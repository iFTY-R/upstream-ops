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
	"github.com/ifty-r/upstream-ops/backend/upstreamcap"
	"gorm.io/gorm"
)

type fakeUpstreamCapability struct {
	keys                   []connector.APIKey
	groups                 []connector.APIKeyGroup
	created                []connector.APIKeyCreateRequest
	updates                []connector.APIKeyUpdateRequest
	updateKeyIDs           []int64
	nextKeyID              int64
	revealByID             map[int64]string
	probeResult            *upstreamcap.ProbeResult
	returnCreatedWithoutID bool
}

func (f *fakeUpstreamCapability) ListAPIKeys(ctx context.Context, channelID uint, query connector.APIKeyQuery) (*connector.APIKeyPage, error) {
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

func (f *fakeUpstreamCapability) ListAPIKeyGroups(ctx context.Context, channelID uint) ([]connector.APIKeyGroup, error) {
	return f.groups, nil
}

func (f *fakeUpstreamCapability) CreateAPIKey(ctx context.Context, channelID uint, req connector.APIKeyCreateRequest) (*connector.APIKey, error) {
	f.nextKeyID++
	key := connector.APIKey{ID: f.nextKeyID, Name: req.Name, Status: "active", Group: req.Group, GroupName: req.Group, GroupID: req.GroupID}
	if req.RemainAmount != nil {
		key.Quota = *req.RemainAmount
	} else if req.RemainQuota != nil {
		key.Quota = float64(*req.RemainQuota)
	} else if req.Quota != nil {
		key.Quota = *req.Quota
	}
	if req.UnlimitedQuota != nil {
		key.UnlimitedQuota = *req.UnlimitedQuota
	}
	if req.ModelLimitsEnabled != nil {
		key.ModelLimitsEnabled = *req.ModelLimitsEnabled
	}
	key.ModelLimits = req.ModelLimits
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

func (f *fakeUpstreamCapability) UpdateAPIKey(ctx context.Context, channelID uint, keyID int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error) {
	f.updates = append(f.updates, req)
	f.updateKeyIDs = append(f.updateKeyIDs, keyID)
	for i := range f.keys {
		if f.keys[i].ID != keyID {
			continue
		}
		if req.Status != nil {
			f.keys[i].Status = *req.Status
		}
		if req.RemainAmount != nil {
			f.keys[i].Quota = *req.RemainAmount
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
		if req.ModelLimitsEnabled != nil {
			f.keys[i].ModelLimitsEnabled = *req.ModelLimitsEnabled
		}
		if req.ModelLimits != nil {
			f.keys[i].ModelLimits = *req.ModelLimits
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

func (f *fakeUpstreamCapability) RevealAPIKey(ctx context.Context, channelID uint, keyID int64) (string, error) {
	if f.revealByID == nil {
		return "sk-probe", nil
	}
	if v := f.revealByID[keyID]; v != "" {
		return v, nil
	}
	return "sk-probe", nil
}

func (f *fakeUpstreamCapability) ProbeOpenAICompatible(ctx context.Context, channelID uint, apiKey string, req upstreamcap.ProbeRequest) (*upstreamcap.ProbeResult, error) {
	if f.probeResult != nil {
		return f.probeResult, nil
	}
	return &upstreamcap.ProbeResult{Success: true, Code: "ok", Message: "探测通过", LatencyMS: 1}, nil
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
	fake := &fakeUpstreamCapability{
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
	fake := &fakeUpstreamCapability{
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
	fake := &fakeUpstreamCapability{
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
	if len(fake.created) != 1 {
		t.Fatalf("created = %d, want probe key only", len(fake.created))
	}
	if fake.created[0].ModelLimitsEnabled != nil || strings.TrimSpace(fake.created[0].ModelLimits) != "" {
		t.Fatalf("probe key should not limit models: %#v", fake.created[0])
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
	fake := &fakeUpstreamCapability{
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
	if fake.updateKeyIDs[0] != 2 || fake.updates[0].RemainAmount == nil || *fake.updates[0].RemainAmount != newAPIProbeRemainAmountUSD {
		t.Fatalf("first update = key %d %#v, want probe quota refill", fake.updateKeyIDs[0], fake.updates[0])
	}
	if fake.updateKeyIDs[1] != 2 || fake.updates[1].Status == nil || *fake.updates[1].Status != "active" {
		t.Fatalf("second update = key %d %#v, want probe status active", fake.updateKeyIDs[1], fake.updates[1])
	}
}

func TestEvaluatePolicyClearsNewAPIProbeKeyModelLimits(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "newapi", Type: storage.ChannelTypeNewAPI, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(11)
	fake := &fakeUpstreamCapability{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04, Quota: newAPIProbeRemainAmountUSD, ModelLimitsEnabled: true, ModelLimits: "gpt-4o-mini"},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	if _, err := rates.Upsert(&storage.RateSnapshot{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()}); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-5.4", ProbeTimeoutSeconds: 3, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	if _, err := svc.EvaluatePolicy(context.Background(), policy.ID); err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if len(fake.updates) < 1 || fake.updateKeyIDs[0] != 2 {
		t.Fatalf("updates = ids %#v reqs %#v, want probe model limit correction first", fake.updateKeyIDs, fake.updates)
	}
	if fake.updates[0].ModelLimitsEnabled == nil || *fake.updates[0].ModelLimitsEnabled {
		t.Fatalf("first update should disable model limits: %#v", fake.updates[0])
	}
	if fake.updates[0].ModelLimits == nil || *fake.updates[0].ModelLimits != "" {
		t.Fatalf("first update should clear model limits: %#v", fake.updates[0])
	}
}

func TestEvaluatePolicyDoesNotCircuitCandidateOnProbeUnauthorized(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "newapi", Type: storage.ChannelTypeNewAPI, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(11)
	fake := &fakeUpstreamCapability{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04, Quota: newAPIProbeRemainAmountUSD},
		},
		groups:      []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID:  map[int64]string{2: "sk-probe"},
		probeResult: &upstreamcap.ProbeResult{Success: false, Code: upstreamcap.ProbeCodeProbeKeyUnauthorized, Message: "探测接口返回 HTTP 401：令牌无效", LatencyMS: 1},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	if _, err := rates.Upsert(&storage.RateSnapshot{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()}); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-5.4", ProbeTimeoutSeconds: 3, FailureThreshold: 1, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	if _, err := svc.EvaluatePolicy(context.Background(), policy.ID); err == nil || !strings.Contains(err.Error(), "探测 API Key 鉴权失败") {
		t.Fatalf("evaluate err = %v, want probe auth failure", err)
	}
	candidate, err := repo.FindCandidate(policy.ID, "fast")
	if err != nil {
		t.Fatalf("find candidate: %v", err)
	}
	if candidate != nil && candidate.Status == "circuit_open" {
		t.Fatalf("candidate should not circuit on unauthorized: %#v", candidate)
	}
}

func TestEvaluatePolicyDoesNotCircuitCandidateOnProbeModelForbidden(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "newapi", Type: storage.ChannelTypeNewAPI, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(11)
	fake := &fakeUpstreamCapability{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04, Quota: newAPIProbeRemainAmountUSD},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID: map[int64]string{2: "sk-probe"},
		probeResult: &upstreamcap.ProbeResult{
			Success:   false,
			Code:      upstreamcap.ProbeCodeProbeModelForbidden,
			Message:   `探测接口返回 HTTP 403：{"error":{"message":"This token has no access to model gpt-5.4"}}`,
			LatencyMS: 1,
		},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	if _, err := rates.Upsert(&storage.RateSnapshot{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()}); err != nil {
		t.Fatalf("upsert rates: %v", err)
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-5.4", ProbeTimeoutSeconds: 3, FailureThreshold: 1, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	if _, err := svc.EvaluatePolicy(context.Background(), policy.ID); err == nil || !strings.Contains(err.Error(), "探测模型无权限") {
		t.Fatalf("evaluate err = %v, want probe model forbidden", err)
	}
	candidate, err := repo.FindCandidate(policy.ID, "fast")
	if err != nil {
		t.Fatalf("find candidate: %v", err)
	}
	if candidate != nil && candidate.Status == "circuit_open" {
		t.Fatalf("candidate should not circuit on probe model forbidden: %#v", candidate)
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
	fake := &fakeUpstreamCapability{
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
	fake := &fakeUpstreamCapability{
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
	if len(fake.updates) != 2 {
		t.Fatalf("updates = %d, want probe key move and target switch", len(fake.updates))
	}
	fast, err := repo.FindCandidate(policy.ID, "fast")
	if err != nil {
		t.Fatalf("find fast candidate: %v", err)
	}
	if fast == nil || fast.Status != "healthy" {
		t.Fatalf("fast candidate = %#v, want probed first by lowest ratio", fast)
	}
	slow, err := repo.FindCandidate(policy.ID, "slow")
	if err != nil {
		t.Fatalf("find slow candidate: %v", err)
	}
	if slow == nil || slow.Status != "unknown" {
		t.Fatalf("slow candidate = %#v, want skipped by budget after lower ratio fast", slow)
	}
	got, err := repo.FindPolicy(policy.ID)
	if err != nil {
		t.Fatalf("find policy: %v", err)
	}
	if got.CurrentGroupName != "fast" || got.CurrentRatio != 0.04 {
		t.Fatalf("current group = %q ratio %.2f, want fast 0.04", got.CurrentGroupName, got.CurrentRatio)
	}
}

func TestEvaluatePolicyTreatsFirstProbeSuccessAsHealthy(t *testing.T) {
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
	fake := &fakeUpstreamCapability{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "slow", GroupID: &slowID, GroupRatio: 0.06},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "slow", GroupID: &slowID, GroupRatio: 0.06},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}, {ID: &slowID, Name: "slow", Ratio: 0.06}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	for _, snapshot := range []storage.RateSnapshot{
		{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()},
		{ChannelID: ch.ID, ModelName: "slow", Ratio: 0.06, LastSeenAt: time.Now()},
	} {
		snapshot := snapshot
		if _, err := rates.Upsert(&snapshot); err != nil {
			t.Fatalf("upsert rates: %v", err)
		}
	}
	policy := &storage.AutoGroupPolicy{
		ChannelID:                     ch.ID,
		Name:                          "auto",
		Enabled:                       true,
		TargetKeyName:                 "auto",
		ProbeKeyName:                  "ops-probe-auto",
		ProbeModel:                    "gpt-4o-mini",
		ProbeTimeoutSeconds:           3,
		ProbeSuccessCacheMinutes:      60,
		ProbeFailureRetryMinutes:      10,
		ProbeMaxPerRun:                1,
		FailureThreshold:              2,
		CircuitDurationMinutes:        30,
		HalfOpenSuccessThreshold:      2,
		KeepCurrentWhenNoAvailable:    true,
		ForceSwitchOnCurrentUnhealthy: true,
	}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	res, err := svc.EvaluatePolicy(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if res.Selected == nil || res.Selected.GroupName != "fast" || res.Selected.Status != "healthy" {
		t.Fatalf("selected = %#v, want fast healthy after first non-circuit probe success", res.Selected)
	}
	got, err := repo.FindCandidate(policy.ID, "fast")
	if err != nil {
		t.Fatalf("find fast: %v", err)
	}
	if got == nil || got.Status != "healthy" {
		t.Fatalf("stored fast = %#v, want healthy", got)
	}
}

func TestEvaluatePolicySwitchesToBetterHealthyCandidateDuringCooldown(t *testing.T) {
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
	fake := &fakeUpstreamCapability{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "slow", GroupID: &slowID, GroupRatio: 0.06},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "slow", GroupID: &slowID, GroupRatio: 0.06},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}, {ID: &slowID, Name: "slow", Ratio: 0.06}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	for _, snapshot := range []storage.RateSnapshot{
		{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()},
		{ChannelID: ch.ID, ModelName: "slow", Ratio: 0.06, LastSeenAt: time.Now()},
	} {
		snapshot := snapshot
		if _, err := rates.Upsert(&snapshot); err != nil {
			t.Fatalf("upsert rates: %v", err)
		}
	}
	lastSwitch := time.Now()
	policy := &storage.AutoGroupPolicy{
		ChannelID:                     ch.ID,
		Name:                          "auto",
		Enabled:                       true,
		NotifyEnabled:                 false,
		TargetKeyName:                 "auto",
		ProbeKeyName:                  "ops-probe-auto",
		ProbeModel:                    "gpt-4o-mini",
		ProbeTimeoutSeconds:           3,
		ProbeSuccessCacheMinutes:      60,
		ProbeFailureRetryMinutes:      10,
		ProbeMaxPerRun:                1,
		FailureThreshold:              2,
		CircuitDurationMinutes:        30,
		HalfOpenSuccessThreshold:      1,
		MinRatioImprovementPct:        5,
		SwitchCooldownMinutes:         30,
		LastSwitchAt:                  &lastSwitch,
		KeepCurrentWhenNoAvailable:    true,
		ForceSwitchOnCurrentUnhealthy: true,
	}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	probeOK := true
	lastProbe := time.Now().Add(-30 * time.Minute)
	for _, c := range []storage.AutoGroupCandidate{
		{PolicyID: policy.ID, GroupName: "fast", GroupID: &fastID, Ratio: 0.04, Status: "healthy", Reason: "探测通过", LastProbeAt: &lastProbe, LastProbeSuccess: &probeOK},
		{PolicyID: policy.ID, GroupName: "slow", GroupID: &slowID, Ratio: 0.06, Status: "healthy", Reason: "探测通过", LastProbeAt: &lastProbe, LastProbeSuccess: &probeOK},
	} {
		c := c
		if err := repo.UpsertCandidate(&c); err != nil {
			t.Fatalf("upsert candidate %s: %v", c.GroupName, err)
		}
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	res, err := svc.EvaluatePolicy(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if res.Selected == nil || res.Selected.GroupName != "fast" || res.Selected.Ratio != 0.04 {
		t.Fatalf("selected = %#v, want fast 0.04", res.Selected)
	}
	if res.EvaluationLog.Status != "switched" {
		t.Fatalf("status = %q action %q, want switched despite cooldown for better ratio", res.EvaluationLog.Status, res.EvaluationLog.Action)
	}
	if len(fake.updates) != 1 || fake.updateKeyIDs[0] != 1 {
		t.Fatalf("updates = ids %#v reqs %#v, want target key switch only", fake.updateKeyIDs, fake.updates)
	}
	got, err := repo.FindPolicy(policy.ID)
	if err != nil {
		t.Fatalf("find policy: %v", err)
	}
	if got.CurrentGroupName != "fast" || got.CurrentRatio != 0.04 {
		t.Fatalf("current group = %q ratio %.2f, want fast 0.04", got.CurrentGroupName, got.CurrentRatio)
	}
}

func TestEvaluatePolicySwitchesNewAPIByGroupName(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "newapi", Type: storage.ChannelTypeNewAPI, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	bestID := int64(1001)
	currentID := int64(1002)
	fake := &fakeUpstreamCapability{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", Group: "特惠分组0.025倍率", GroupName: "特惠分组0.025倍率", GroupRatio: 0.025},
			{ID: 2, Name: "ops-probe-auto", Status: "active", Group: "特惠分组0.025倍率", GroupName: "特惠分组0.025倍率", GroupRatio: 0.025, Quota: newAPIProbeRemainAmountUSD},
		},
		groups: []connector.APIKeyGroup{
			{ID: &bestID, Name: "特惠分组0.01倍率", Ratio: 0.01},
			{ID: &currentID, Name: "特惠分组0.025倍率", Ratio: 0.025},
		},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	for _, snapshot := range []storage.RateSnapshot{
		{ChannelID: ch.ID, ModelName: "特惠分组0.01倍率", Ratio: 0.01, LastSeenAt: time.Now()},
		{ChannelID: ch.ID, ModelName: "特惠分组0.025倍率", Ratio: 0.025, LastSeenAt: time.Now()},
	} {
		snapshot := snapshot
		if _, err := rates.Upsert(&snapshot); err != nil {
			t.Fatalf("upsert rates: %v", err)
		}
	}
	policy := &storage.AutoGroupPolicy{
		ChannelID:                     ch.ID,
		Name:                          "WorldClawPro",
		Enabled:                       true,
		NotifyEnabled:                 false,
		TargetKeyName:                 "auto",
		ProbeKeyName:                  "ops-probe-auto",
		ProbeModel:                    "gpt-5.4",
		ProbeTimeoutSeconds:           3,
		ProbeSuccessCacheMinutes:      60,
		ProbeFailureRetryMinutes:      10,
		ProbeMaxPerRun:                3,
		FailureThreshold:              2,
		CircuitDurationMinutes:        30,
		HalfOpenSuccessThreshold:      1,
		MinRatioImprovementPct:        5,
		SwitchCooldownMinutes:         30,
		KeepCurrentWhenNoAvailable:    true,
		ForceSwitchOnCurrentUnhealthy: true,
	}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	probeOK := true
	lastProbe := time.Now().Add(-30 * time.Minute)
	for _, c := range []storage.AutoGroupCandidate{
		{PolicyID: policy.ID, GroupName: "特惠分组0.01倍率", GroupID: &bestID, Ratio: 0.01, Status: "healthy", Reason: "探测通过", LastProbeAt: &lastProbe, LastProbeSuccess: &probeOK},
		{PolicyID: policy.ID, GroupName: "特惠分组0.025倍率", GroupID: &currentID, Ratio: 0.025, Status: "healthy", Reason: "探测通过", LastProbeAt: &lastProbe, LastProbeSuccess: &probeOK},
	} {
		c := c
		if err := repo.UpsertCandidate(&c); err != nil {
			t.Fatalf("upsert candidate %s: %v", c.GroupName, err)
		}
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	res, err := svc.EvaluatePolicy(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if res.EvaluationLog.Status != "switched" || res.Selected == nil || res.Selected.GroupName != "特惠分组0.01倍率" {
		t.Fatalf("status = %q selected = %#v, want switched to best newapi group", res.EvaluationLog.Status, res.Selected)
	}
	if len(fake.updates) != 1 || fake.updates[0].Group == nil || *fake.updates[0].Group != "特惠分组0.01倍率" {
		t.Fatalf("update req = %#v, want group name for newapi", fake.updates)
	}
	if fake.updates[0].GroupID != nil {
		t.Fatalf("newapi update used group_id = %v, want nil", *fake.updates[0].GroupID)
	}
	got, err := repo.FindPolicy(policy.ID)
	if err != nil {
		t.Fatalf("find policy: %v", err)
	}
	if got.CurrentGroupName != "特惠分组0.01倍率" || got.CurrentRatio != 0.01 {
		t.Fatalf("current group = %q ratio %.3f, want best group 0.01", got.CurrentGroupName, got.CurrentRatio)
	}
}

func TestEvaluatePolicyRemovesCandidatesMissingFromUpstreamGroups(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(1)
	oldID := int64(2)
	fake := &fakeUpstreamCapability{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	rates := storage.NewRates(db)
	for _, snapshot := range []storage.RateSnapshot{
		{ChannelID: ch.ID, ModelName: "fast", Ratio: 0.04, LastSeenAt: time.Now()},
		{ChannelID: ch.ID, ModelName: "old", Ratio: 0.01, LastSeenAt: time.Now()},
	} {
		snapshot := snapshot
		if _, err := rates.Upsert(&snapshot); err != nil {
			t.Fatalf("upsert rates: %v", err)
		}
	}
	policy := &storage.AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", Enabled: true, NotifyEnabled: false, TargetKeyName: "auto", ProbeKeyName: "ops-probe-auto", ProbeModel: "gpt-4o-mini", ProbeTimeoutSeconds: 3, ProbeSuccessCacheMinutes: 60, FailureThreshold: 2, CircuitDurationMinutes: 30, HalfOpenSuccessThreshold: 1, KeepCurrentWhenNoAvailable: true, ForceSwitchOnCurrentUnhealthy: true}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	probeOK := true
	lastProbe := time.Now().Add(-10 * time.Minute)
	if err := repo.UpsertCandidate(&storage.AutoGroupCandidate{PolicyID: policy.ID, GroupName: "old", GroupID: &oldID, Ratio: 0.01, Status: "healthy", LastProbeAt: &lastProbe, LastProbeSuccess: &probeOK}); err != nil {
		t.Fatalf("upsert stale candidate: %v", err)
	}

	svc := NewService(repo, channels, rates, fake, nil, nil)
	res, err := svc.EvaluatePolicy(context.Background(), policy.ID)
	if err != nil {
		t.Fatalf("evaluate policy: %v", err)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].GroupName != "fast" {
		t.Fatalf("candidates = %#v, want only current upstream group fast", res.Candidates)
	}
	stale, err := repo.FindCandidate(policy.ID, "old")
	if err != nil {
		t.Fatalf("find stale candidate: %v", err)
	}
	if stale != nil {
		t.Fatalf("stale candidate still exists: %#v", stale)
	}
	list, err := repo.ListCandidates(policy.ID)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(list) != 1 || list[0].GroupName != "fast" {
		t.Fatalf("stored candidates = %#v, want only fast", list)
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
	fake := &fakeUpstreamCapability{
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

func TestSetCandidateManualDisabledRefreshesRestoredStatus(t *testing.T) {
	db := openAutoGroupTestDB(t)

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: "https://example.invalid", Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	repo := storage.NewAutoGroups(db)
	policy := &storage.AutoGroupPolicy{
		ChannelID:                ch.ID,
		Name:                     "auto",
		Enabled:                  true,
		TargetKeyName:            "auto",
		ProbeKeyName:             "ops-probe-auto",
		ProbeModel:               "gpt-4o-mini",
		ProbeSuccessCacheMinutes: 60,
		FailureThreshold:         2,
		CircuitDurationMinutes:   30,
		HalfOpenSuccessThreshold: 1,
	}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	gid := int64(1)
	probeOK := true
	lastProbe := time.Now().Add(-10 * time.Minute)
	if err := repo.UpsertCandidate(&storage.AutoGroupCandidate{
		PolicyID:         policy.ID,
		GroupName:        "fast",
		GroupID:          &gid,
		Ratio:            0.04,
		Status:           "excluded",
		Reason:           "已手动停用",
		ManualDisabled:   true,
		LastProbeAt:      &lastProbe,
		LastProbeSuccess: &probeOK,
	}); err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	candidate, err := repo.FindCandidate(policy.ID, "fast")
	if err != nil {
		t.Fatalf("find candidate: %v", err)
	}

	svc := NewService(repo, channels, nil, nil, nil, nil)
	restored, err := svc.SetCandidateManualDisabled(policy.ID, candidate.ID, false)
	if err != nil {
		t.Fatalf("restore candidate: %v", err)
	}
	if restored == nil || restored.ManualDisabled || restored.Status != "healthy" {
		t.Fatalf("restored candidate = %#v, want healthy and enabled", restored)
	}
}

func TestUpdatePolicyRefreshesCandidateRuleStatus(t *testing.T) {
	db := openAutoGroupTestDB(t)

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: "https://example.invalid", Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	repo := storage.NewAutoGroups(db)
	svc := NewService(repo, channels, nil, nil, nil, nil)
	forceSwitch := true
	keepCurrent := true
	view, err := svc.CreatePolicy(PolicyInput{
		ChannelID:                     ch.ID,
		Name:                          "auto",
		Enabled:                       true,
		TargetKeyName:                 "auto",
		ProbeKeyName:                  "ops-probe-auto",
		ProbeModel:                    "gpt-4o-mini",
		ExcludeGroups:                 []string{"fast"},
		FailureThreshold:              2,
		CircuitDurationMinutes:        30,
		HalfOpenSuccessThreshold:      1,
		ForceSwitchOnCurrentUnhealthy: &forceSwitch,
		KeepCurrentWhenNoAvailable:    &keepCurrent,
	})
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}

	fastID := int64(1)
	manualID := int64(2)
	probeOK := true
	lastProbe := time.Now().Add(-time.Minute)
	if err := repo.UpsertCandidate(&storage.AutoGroupCandidate{
		PolicyID:         view.ID,
		GroupName:        "fast",
		GroupID:          &fastID,
		Ratio:            0.5,
		Status:           "excluded",
		Reason:           "命中排除分组",
		LastProbeAt:      &lastProbe,
		LastProbeSuccess: &probeOK,
	}); err != nil {
		t.Fatalf("upsert fast candidate: %v", err)
	}
	if err := repo.UpsertCandidate(&storage.AutoGroupCandidate{
		PolicyID:       view.ID,
		GroupName:      "manual",
		GroupID:        &manualID,
		Ratio:          0.4,
		Status:         "excluded",
		Reason:         "已手动停用",
		ManualDisabled: true,
	}); err != nil {
		t.Fatalf("upsert manual candidate: %v", err)
	}

	updated, err := svc.UpdatePolicy(view.ID, PolicyInput{
		ChannelID:                     ch.ID,
		Name:                          "auto",
		Enabled:                       true,
		TargetKeyName:                 "auto",
		ProbeKeyName:                  "ops-probe-auto",
		ProbeModel:                    "gpt-4o-mini",
		FailureThreshold:              2,
		CircuitDurationMinutes:        30,
		HalfOpenSuccessThreshold:      1,
		ForceSwitchOnCurrentUnhealthy: &forceSwitch,
		KeepCurrentWhenNoAvailable:    &keepCurrent,
	})
	if err != nil {
		t.Fatalf("update policy: %v", err)
	}

	fast, err := repo.FindCandidate(view.ID, "fast")
	if err != nil {
		t.Fatalf("find fast candidate: %v", err)
	}
	if fast == nil || fast.Status != "healthy" {
		t.Fatalf("fast candidate = %#v, want healthy after removing exclude rule", fast)
	}
	manual, err := repo.FindCandidate(view.ID, "manual")
	if err != nil {
		t.Fatalf("find manual candidate: %v", err)
	}
	if manual == nil || manual.Status != "excluded" || !manual.ManualDisabled {
		t.Fatalf("manual candidate = %#v, want still manually excluded", manual)
	}

	foundFastInResponse := false
	for _, c := range updated.Candidates {
		if c.GroupName == "fast" {
			foundFastInResponse = true
			if c.Status != "healthy" {
				t.Fatalf("response fast status = %q, want healthy", c.Status)
			}
		}
	}
	if !foundFastInResponse {
		t.Fatalf("updated policy response did not include fast candidate")
	}
}

func TestEvaluatePolicyReturnsRateSnapshotError(t *testing.T) {
	db := openAutoGroupTestDB(t)
	server := newProbeServer(t)
	defer server.Close()

	channels := storage.NewChannels(db)
	ch := &storage.Channel{Name: "sub", Type: storage.ChannelTypeSub2API, SiteURL: server.URL, Username: "u", PasswordCipher: "x", MonitorEnabled: true}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	fastID := int64(1)
	fake := &fakeUpstreamCapability{
		keys: []connector.APIKey{
			{ID: 1, Name: "auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
			{ID: 2, Name: "ops-probe-auto", Status: "active", GroupName: "fast", GroupID: &fastID, GroupRatio: 0.04},
		},
		groups:     []connector.APIKeyGroup{{ID: &fastID, Name: "fast", Ratio: 0.04}},
		revealByID: map[int64]string{2: "sk-probe"},
	}
	repo := storage.NewAutoGroups(db)
	policy := &storage.AutoGroupPolicy{
		ChannelID:                     ch.ID,
		Name:                          "auto",
		Enabled:                       true,
		TargetKeyName:                 "auto",
		ProbeKeyName:                  "ops-probe-auto",
		ProbeModel:                    "gpt-4o-mini",
		ProbeTimeoutSeconds:           3,
		FailureThreshold:              2,
		CircuitDurationMinutes:        30,
		HalfOpenSuccessThreshold:      1,
		KeepCurrentWhenNoAvailable:    true,
		ForceSwitchOnCurrentUnhealthy: true,
	}
	if err := repo.CreatePolicy(policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	badDB := openAutoGroupTestDB(t)
	sqlDB, err := badDB.DB()
	if err != nil {
		t.Fatalf("get bad sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close bad db: %v", err)
	}

	svc := NewService(repo, channels, storage.NewRates(badDB), fake, nil, nil)
	_, err = svc.EvaluatePolicy(context.Background(), policy.ID)
	if err == nil || !strings.Contains(err.Error(), "读取倍率快照失败") {
		t.Fatalf("evaluate err = %v, want rate snapshot error", err)
	}
	got, err := repo.FindPolicy(policy.ID)
	if err != nil {
		t.Fatalf("find policy: %v", err)
	}
	if got.LastStatus != "failed" || !strings.Contains(got.LastError, "读取倍率快照失败") {
		t.Fatalf("policy status = %q err = %q, want failed rate snapshot error", got.LastStatus, got.LastError)
	}
}
