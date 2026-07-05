package autogroup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/connector"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

type fakeChannelService struct {
	keys                   []connector.APIKey
	groups                 []connector.APIKeyGroup
	created                []connector.APIKeyCreateRequest
	updates                []connector.APIKeyUpdateRequest
	updateKeyIDs           []int64
	nextKeyID              int64
	revealByID             map[int64]string
	returnCreatedWithoutID bool
}

func (f *fakeChannelService) ListAPIKeys(ctx context.Context, channelID uint, query connector.APIKeyQuery) (*connector.APIKeyPage, error) {
	items := f.keys
	if strings.TrimSpace(query.Search) != "" {
		needle := strings.ToLower(strings.TrimSpace(query.Search))
		filtered := make([]connector.APIKey, 0, len(items))
		for _, key := range items {
			if strings.Contains(strings.ToLower(key.Name), needle) {
				filtered = append(filtered, key)
			}
		}
		items = filtered
	}
	return &connector.APIKeyPage{Items: items, Total: int64(len(items)), Page: 1, PageSize: len(items), Pages: 1}, nil
}

func (f *fakeChannelService) ListAPIKeyGroups(ctx context.Context, channelID uint) ([]connector.APIKeyGroup, error) {
	return f.groups, nil
}

func (f *fakeChannelService) CreateAPIKey(ctx context.Context, channelID uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error) {
	f.nextKeyID++
	key := connector.APIKey{ID: f.nextKeyID, Name: req.Name, Status: "active", Group: req.Group, GroupName: req.Group, GroupID: req.GroupID}
	if req.RemainQuota != nil {
		key.Quota = float64(*req.RemainQuota)
	} else if req.Quota != nil {
		key.Quota = *req.Quota
	}
	if req.UnlimitedQuota != nil {
		key.UnlimitedQuota = *req.UnlimitedQuota
	}
	if req.GroupID != nil {
		for _, group := range f.groups {
			if group.ID != nil && *group.ID == *req.GroupID {
				key.Group = group.Name
				key.GroupName = group.Name
				key.GroupRatio = group.Ratio
			}
		}
	}
	f.created = append(f.created, req)
	f.keys = append(f.keys, key)
	if f.revealByID == nil {
		f.revealByID = map[int64]string{}
	}
	f.revealByID[key.ID] = "sk-" + req.Name
	if f.returnCreatedWithoutID {
		withoutID := key
		withoutID.ID = 0
		return &withoutID, nil
	}
	return &key, nil
}

func (f *fakeChannelService) UpdateAPIKey(ctx context.Context, channelID uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error) {
	f.updates = append(f.updates, req)
	f.updateKeyIDs = append(f.updateKeyIDs, keyID)
	for i := range f.keys {
		if f.keys[i].ID != keyID {
			continue
		}
		if req.Status != nil {
			f.keys[i].Status = *req.Status
		}
		if req.RemainQuota != nil {
			f.keys[i].Quota = float64(*req.RemainQuota)
		}
		if req.Quota != nil {
			f.keys[i].Quota = *req.Quota
		}
		if req.UnlimitedQuota != nil {
			f.keys[i].UnlimitedQuota = *req.UnlimitedQuota
		}
		if req.Group != nil {
			f.keys[i].Group = *req.Group
			f.keys[i].GroupName = *req.Group
		}
		if req.GroupID != nil {
			f.keys[i].GroupID = req.GroupID
			for _, group := range f.groups {
				if group.ID != nil && *group.ID == *req.GroupID {
					f.keys[i].Group = group.Name
					f.keys[i].GroupName = group.Name
					f.keys[i].GroupRatio = group.Ratio
				}
			}
		}
		return &f.keys[i], nil
	}
	return nil, nil
}

func (f *fakeChannelService) RevealAPIKey(ctx context.Context, channelID uint, keyID int64) (string, error) {
	if f.revealByID == nil {
		return "sk-probe", nil
	}
	if v := f.revealByID[keyID]; v != "" {
		return v, nil
	}
	return "sk-probe", nil
}

func openAutoGroupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func newProbeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "probe-ok"})
	}))
}

func TestEvaluatePolicyCreatesMissingTargetAutoKey(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	gid := int64(1)
	fake := &fakeChannelService{
		nextKeyID: 10,
		groups:    []connector.APIKeyGroup{{ID: &gid, Name: "fast", Ratio: 0.5}},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	if _, err := rates.Upsert(&storage.RateSnapshot{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.5, LastSeenAt: time.Now()}); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-4o-mini", ProbeTimeoutSeconds: 3, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	if _, err := svc.EvaluatePolicy(context.Background(), policy.ID); err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}

	if len(fake.created) < 2 {
		t.Fatalf("created keys = %d, want target and probe", len(fake.created))
	}
	if fake.created[0].Name != "auto" {
		t.Fatalf("first created key = %q, want auto", fake.created[0].Name)
	}
	got, err := repo.FindPolicy(policy.ID)
	if err != nil {
		t.Fatalf("find policy: %v", err)
	}
	if got.TargetKeyID == 0 || got.TargetKeyName != "auto" {
		t.Fatalf("target key not persisted: %#v", got)
	}
}

func TestEvaluatePolicyBackfillsCurrentGroupNameFromGroupID(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(11)
	fake := &fakeChannelService{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupID: &fastID, GroupRatio: 0.04},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupID: &fastID, GroupRatio: 0.04},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	if _, err := rates.Upsert(&storage.RateSnapshot{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()}); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-4o-mini", ProbeTimeoutSeconds: 3, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	res, err := svc.EvaluatePolicy(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if res.EvaluationLog.CurrentGroup != "fast" {
		t.Fatalf("log current group = %q, want fast", res.EvaluationLog.CurrentGroup)
	}
	got, err := repo.FindPolicy(policy.ID)
	if err != nil {
		t.Fatalf("find policy: %v", err)
	}
	if got.CurrentGroupName != "fast" || got.CurrentGroupID == nil || *got.CurrentGroupID != fastID || got.CurrentRatio != 0.04 {
		t.Fatalf("current group not backfilled: %#v", got)
	}
}

func TestEvaluatePolicyBackfillsProbeKeyIDAfterInvalidCreateResponse(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "newapi", Type: storage.ChannelTypeNewAPI, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(11)
	fake := &fakeChannelService{
		nextKeyID: 20,
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
		},
		groups:                 []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID:             map[int64]string{21: "sk-probe"},
		returnCreatedWithoutID: true,
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	if _, err := rates.Upsert(&storage.RateSnapshot{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()}); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-4o-mini", ProbeTimeoutSeconds: 3, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	if _, err := svc.EvaluatePolicy(context.Background(), policy.ID); err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	got, err := repo.FindPolicy(policy.ID)
	if err != nil {
		t.Fatalf("find policy: %v", err)
	}
	if got.ProbeKeyID != 21 || got.ProbeKeyName != "ops-probe-auto" {
		t.Fatalf("probe key not backfilled: %#v", got)
	}
}

func TestEvaluatePolicyRecoversExhaustedNewAPIProbeKey(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "newapi", Type: storage.ChannelTypeNewAPI, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(11)
	fake := &fakeChannelService{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
			{ID: 2, Name: "ops-probe-auto", Status: "quota_exhausted", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04, Quota: 0},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	if _, err := rates.Upsert(&storage.RateSnapshot{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()}); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-4o-mini", ProbeTimeoutSeconds: 3, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	if _, err := svc.EvaluatePolicy(context.Background(), policy.ID); err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if len(fake.updates) < 2 {
		t.Fatalf("updates = %d, want quota refill and status restore", len(fake.updates))
	}
	if fake.updateKeyIDs[0] != 2 || fake.updates[0].RemainQuota == nil || *fake.updates[0].RemainQuota != newAPIProbeRemainQuota {
		t.Fatalf("first update = key %d %#v, want probe quota refill", fake.updateKeyIDs[0], fake.updates[0])
	}
	if fake.updateKeyIDs[1] != 2 || fake.updates[1].Status == nil || *fake.updates[1].Status != "active" {
		t.Fatalf("second update = key %d %#v, want probe status active", fake.updateKeyIDs[1], fake.updates[1])
	}
}

func TestEvaluatePolicyUsesProbeSuccessCache(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(1)
	fake := &fakeChannelService{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	if _, err := rates.Upsert(&storage.RateSnapshot{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()}); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-4o-mini", ProbeTimeoutSeconds: 3, ProbeSuccessCacheMinutes: 60, ProbeFailureRetryMinutes: 10, ProbeMaxPerRun: 3, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	probeOK := true
	lastProbe := time.Now().Add(-5 * time.Minute)
	if err := repo.UpsertCandidate(&storage.AutoGroupCandidate{PolicyID: policy.ID, GroupName: "fast", GroupID: &fastID, Ratio: 0.04, Status: "healthy", LastProbeAt: &lastProbe, LastProbeSuccess: &probeOK}); err != nil {
		t.Fatalf("upsert cached candidate: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	if _, err := svc.EvaluatePolicy(context.Background(), policy.ID); err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if len(fake.updates) != 0 {
		t.Fatalf("updates = %d, want no probe key move when cache is valid", len(fake.updates))
	}
}

func TestEvaluatePolicyHonorsProbeBudget(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(1)
	slowID := int64(2)
	fake := &fakeChannelService{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "slow", GroupID: &slowID, GroupRatio: 1},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "slow", GroupID: &slowID, GroupRatio: 1},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}, {ID: &slowID, Name: "slow", Ratio: 1}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	for _, snapshot := range []storage.RateSnapshot{
		{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()},
		{ChannelID: ch.ID, ModelName: "slow", Ratio: 1, LastSeenAt: time.Now()},
	} {
		snapshot := snapshot
		if _, err := rates.Upsert(&snapshot); err != nil {
			t.Fatalf("upsert rates: %v", err)
		}
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-4o-mini", ProbeTimeoutSeconds: 3, ProbeSuccessCacheMinutes: 60, ProbeFailureRetryMinutes: 10, ProbeMaxPerRun: 1, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	if _, err := svc.EvaluatePolicy(context.Background(), policy.ID); err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if len(fake.updates) != 1 {
		t.Fatalf("updates = %d, want only one probe key move", len(fake.updates))
	}
	fast, err := repo.FindCandidate(policy.ID, "fast")
	if err != nil {
		t.Fatalf("find fast candidate: %v", err)
	}
	if fast == nil || fast.Status != "unknown" {
		t.Fatalf("fast candidate = %#v, want skipped by budget", fast)
	}
}

func TestManualDisabledCandidateIsExcludedFromSelection(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(1)
	slowID := int64(2)
	fake := &fakeChannelService{
		nextKeyID: 20,
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "slow", GroupID: &slowID, GroupRatio: 1},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "slow", GroupID: &slowID, GroupRatio: 1},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.5}, {ID: &slowID, Name: "slow", Ratio: 1}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	for _, snapshot := range []storage.RateSnapshot{
		{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.5, LastSeenAt: time.Now()},
		{ChannelID: ch.ID, ModelName: "slow", Ratio: 1, LastSeenAt: time.Now()},
	} {
		snapshot := snapshot
		if _, err := rates.Upsert(&snapshot); err != nil {
			t.Fatalf("upsert rates: %v", err)
		}
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-4o-mini", ProbeTimeoutSeconds: 3, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	if err := repo.UpsertCandidate(&storage.AutoGroupCandidate{PolicyID: policy.ID, GroupName: "fast", Ratio: 0.5, ManualDisabled: true}); err != nil {
		t.Fatalf("upsert disabled candidate: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	res, err := svc.EvaluatePolicy(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if res.Selected == nil || res.Selected.GroupName != "slow" {
		t.Fatalf("selected = %#v, want slow", res.Selected)
	}
}
