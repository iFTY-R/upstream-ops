package shopmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ifty-r/upstream-ops/backend/config"
	"github.com/ifty-r/upstream-ops/backend/notify"
	"github.com/ifty-r/upstream-ops/backend/shopprovider"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

type Service struct {
	targets    *storage.ShopTargets
	watchRules *storage.ShopWatchRules
	goods      *storage.ShopGoods
	dispatcher *notify.Dispatcher
	log        *slog.Logger
	mu         sync.RWMutex
	locks      sync.Map
	proxy      config.ProxyConfig
	upstream   config.UpstreamConfig
}

const (
	shopGoodsPageSize = 50
	maxShopGoodsPages = 1000
)

func NewService(
	targets *storage.ShopTargets,
	watchRules *storage.ShopWatchRules,
	goods *storage.ShopGoods,
	dispatcher *notify.Dispatcher,
	log *slog.Logger,
	proxy config.ProxyConfig,
	upstream config.UpstreamConfig,
) *Service {
	return &Service{
		targets:    targets,
		watchRules: watchRules,
		goods:      goods,
		dispatcher: dispatcher,
		log:        log,
		proxy:      proxy,
		upstream:   upstream.WithDefaults(),
	}
}

func (s *Service) UpdateProxyConfig(cfg config.ProxyConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proxy = cfg
}

func (s *Service) UpdateUpstreamConfig(cfg config.UpstreamConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstream = cfg.WithDefaults()
}

type TestResult struct {
	Info       *shopprovider.ShopInfo  `json:"info"`
	Categories []shopprovider.Category `json:"categories"`
}

type SyncResult struct {
	GoodsCount   int            `json:"goods_count"`
	ChangedCount int            `json:"changed_count"`
	Events       map[string]int `json:"events"`
}

type RefreshGoodsResult struct {
	Snapshot *storage.ShopGoodsSnapshot `json:"snapshot"`
	Found    bool                       `json:"found"`
	Changed  bool                       `json:"changed"`
}

type changeDraft struct {
	item     shopprovider.Goods
	event    storage.ShopGoodsChangeEvent
	oldValue string
	newValue string
	summary  string
}

type SyncAllTargetResult struct {
	TargetID uint        `json:"target_id"`
	Name     string      `json:"name"`
	Result   *SyncResult `json:"result,omitempty"`
	Error    string      `json:"error,omitempty"`
}

type SyncAllResult struct {
	Total   int                   `json:"total"`
	Success int                   `json:"success"`
	Failed  int                   `json:"failed"`
	Targets []SyncAllTargetResult `json:"targets"`
}

func (s *Service) ParseURL(siteURL string) (*shopprovider.ParsedURL, error) {
	return shopprovider.ParseShopURL(siteURL)
}

func (s *Service) Test(ctx context.Context, target storage.ShopTarget) (*TestResult, error) {
	provider, starget, err := s.providerFor(&target)
	if err != nil {
		return nil, err
	}
	info, err := provider.Info(ctx, starget)
	if err != nil {
		return nil, err
	}
	categories, err := provider.Categories(ctx, starget, shopprovider.CategoryRequest{GoodsType: firstGoodsType(target.GoodsTypesJSON)})
	if err != nil {
		return nil, err
	}
	return &TestResult{Info: info, Categories: categories}, nil
}

func (s *Service) SyncByID(ctx context.Context, id uint) (*SyncResult, error) {
	target, err := s.targets.FindByID(id)
	if err != nil {
		return nil, err
	}
	return s.Sync(ctx, target)
}

func (s *Service) RefreshGoodsByKey(ctx context.Context, targetID uint, goodsKey string) (*RefreshGoodsResult, error) {
	goodsKey = strings.TrimSpace(goodsKey)
	if goodsKey == "" {
		return nil, fmt.Errorf("goods key is required")
	}
	target, err := s.targets.FindByID(targetID)
	if err != nil {
		return nil, err
	}
	unlock := s.lockTarget(target.ID)
	defer unlock()
	target, err = s.targets.FindByID(targetID)
	if err != nil {
		return nil, err
	}
	provider, starget, err := s.providerFor(target)
	if err != nil {
		return nil, err
	}
	current, err := s.goods.FindSnapshot(targetID, goodsKey)
	if err != nil {
		return nil, err
	}
	item, found, err := s.fetchSingleGoods(ctx, provider, starget, current, goodsKey)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if !found {
		if current == nil {
			return nil, fmt.Errorf("goods %s not found", goodsKey)
		}
		changed := current.RemovedAt == nil
		if changed {
			removedAt := now
			current.RemovedAt = &removedAt
			current.LastChangedAt = &now
			if err := s.goods.SaveSnapshot(current); err != nil {
				return nil, err
			}
		}
		return &RefreshGoodsResult{Snapshot: current, Found: false, Changed: changed}, nil
	}

	next := snapshotFromGoods(targetID, *item, now)
	changed := true
	if current != nil {
		changed = shopSnapshotChanged(*current, next) || current.RemovedAt != nil
		next.ID = current.ID
		next.FirstSeenAt = current.FirstSeenAt
		if changed {
			next.LastChangedAt = &now
		} else {
			next.LastChangedAt = current.LastChangedAt
		}
		if err := s.goods.SaveSnapshot(&next); err != nil {
			return nil, err
		}
	} else if err := s.goods.CreateSnapshot(&next); err != nil {
		return nil, err
	}
	return &RefreshGoodsResult{Snapshot: &next, Found: true, Changed: changed}, nil
}

func (s *Service) SyncAll(ctx context.Context) *SyncAllResult {
	return s.SyncAllWithConcurrency(ctx, 1)
}

// SyncAllWithConcurrency scans independent shops with bounded parallelism.
// Results retain the configured target order so callers can display a stable summary.
func (s *Service) SyncAllWithConcurrency(ctx context.Context, concurrency int) *SyncAllResult {
	out := &SyncAllResult{}
	list, err := s.targets.ListMonitorEnabled()
	if err != nil {
		if s.log != nil {
			s.log.Warn("list shop targets failed", "err", err)
		}
		out.Failed = 1
		out.Targets = append(out.Targets, SyncAllTargetResult{Error: err.Error()})
		return out
	}
	out.Total = len(list)
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(list) {
		concurrency = len(list)
	}
	if concurrency == 0 {
		return out
	}

	out.Targets = make([]SyncAllTargetResult, len(list))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				target := list[index]
				result, syncErr := s.Sync(ctx, &target)
				item := SyncAllTargetResult{TargetID: target.ID, Name: target.Name, Result: result}
				if syncErr != nil {
					item.Error = syncErr.Error()
					if s.log != nil {
						s.log.Warn("shop sync failed", "target", target.Name, "err", syncErr)
					}
				}
				out.Targets[index] = item
			}
		}()
	}
	for index := range list {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	for _, item := range out.Targets {
		if item.Error == "" {
			out.Success++
		} else {
			out.Failed++
		}
	}
	return out
}

func (s *Service) Sync(ctx context.Context, target *storage.ShopTarget) (*SyncResult, error) {
	started := time.Now()
	result := &SyncResult{Events: map[string]int{}}
	if target == nil {
		return result, fmt.Errorf("shop target is nil")
	}
	unlock := s.lockTarget(target.ID)
	defer unlock()
	// The target may have been deleted while this caller waited for its lock.
	currentTarget, err := s.targets.FindByID(target.ID)
	if err != nil {
		return result, err
	}
	target = currentTarget

	provider, starget, err := s.providerFor(target)
	if err != nil {
		s.recordFailure(target, started, err)
		return result, err
	}

	info, err := provider.Info(ctx, starget)
	if err != nil {
		s.recordFailure(target, started, err)
		s.notifyFailure(ctx, target, err)
		return result, err
	}

	fetched, err := s.fetchGoods(ctx, provider, starget, target)
	if err != nil {
		s.recordFailure(target, started, err)
		s.notifyFailure(ctx, target, err)
		return result, err
	}
	result.GoodsCount = len(fetched)

	changes, lowStockCount, err := s.diffAndSave(target, fetched, started)
	if err != nil {
		s.recordFailure(target, started, err)
		return result, err
	}
	result.ChangedCount = len(changes)
	for _, change := range changes {
		result.Events[string(change.Event)]++
	}

	finished := time.Now()
	if err := s.targets.SetSyncResult(target.ID, &finished, "", info.Name, result.GoodsCount, lowStockCount, result.ChangedCount); err != nil {
		return result, fmt.Errorf("保存店铺同步结果失败：%w", err)
	}
	if err := s.goods.AppendMonitorLog(&storage.ShopMonitorLog{
		TargetID:     target.ID,
		Success:      true,
		GoodsCount:   result.GoodsCount,
		ChangedCount: result.ChangedCount,
		StartedAt:    started,
		FinishedAt:   finished,
	}); err != nil {
		return result, fmt.Errorf("记录店铺监控日志失败：%w", err)
	}
	if len(changes) > 0 {
		if err := s.dispatchChanges(ctx, target, info, changes); err != nil && s.log != nil {
			s.log.Warn("dispatch shop changes failed", "target_id", target.ID, "err", err)
		}
	}
	return result, nil
}

// DeleteTarget shares the per-target lock with sync and refresh operations.
// This prevents a completed delete from being followed by stale background writes.
func (s *Service) DeleteTarget(id uint) error {
	if id == 0 {
		return fmt.Errorf("shop target id is required")
	}
	unlock := s.lockTarget(id)
	defer unlock()
	return s.targets.Delete(id)
}

func (s *Service) lockTarget(targetID uint) func() {
	lock, _ := s.locks.LoadOrStore(targetID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Service) providerFor(target *storage.ShopTarget) (shopprovider.Provider, shopprovider.Target, error) {
	provider, err := shopprovider.For(target.Platform)
	if err != nil {
		return nil, shopprovider.Target{}, err
	}
	s.mu.RLock()
	upstream := s.upstream
	proxy := s.proxy
	s.mu.RUnlock()
	if setter, ok := provider.(shopprovider.HTTPConfigSetter); ok {
		upstream = upstream.WithDefaults()
		setter.SetHTTPConfig(shopprovider.HTTPConfig{
			Timeout:   time.Duration(upstream.TimeoutSeconds) * time.Second,
			UserAgent: upstream.UserAgent,
		})
	}
	if target.ProxyEnabled {
		proxyURL, err := proxy.ActiveURL()
		if err != nil {
			return nil, shopprovider.Target{}, err
		}
		if proxyURL != "" {
			if setter, ok := provider.(shopprovider.ProxySetter); ok {
				setter.SetProxy(proxyURL)
			}
		}
	}
	return provider, shopprovider.Target{
		ID:       target.ID,
		Name:     target.Name,
		Platform: target.Platform,
		SiteURL:  target.SiteURL,
		BaseURL:  target.BaseURL,
		Token:    target.Token,
	}, nil
}

func (s *Service) fetchGoods(ctx context.Context, provider shopprovider.Provider, starget shopprovider.Target, target *storage.ShopTarget) (map[string]shopprovider.Goods, error) {
	goodsTypes := parseStringList(target.GoodsTypesJSON)
	if len(goodsTypes) == 0 {
		goodsTypes = []string{"card"}
	}
	out := map[string]shopprovider.Goods{}
	wantedKeys := map[string]struct{}{}
	if target.ScopeMode == storage.ShopScopeGoodsKeys {
		for _, key := range parseStringList(target.GoodsKeysJSON) {
			wantedKeys[key] = struct{}{}
		}
	}
	for _, goodsType := range goodsTypes {
		requests, err := s.buildGoodsRequests(ctx, provider, starget, target, goodsType)
		if err != nil {
			return nil, err
		}
		for _, req := range requests {
			page := 1
			for {
				req.Page = page
				req.PageSize = shopGoodsPageSize
				res, err := provider.Goods(ctx, starget, req)
				if err != nil {
					return nil, err
				}
				for _, item := range res.List {
					if strings.TrimSpace(item.GoodsKey) == "" {
						continue
					}
					if len(wantedKeys) > 0 {
						if _, ok := wantedKeys[item.GoodsKey]; !ok {
							continue
						}
					}
					out[item.GoodsKey] = item
				}
				hasMore, err := hasMoreShopGoodsPages(page, req.PageSize, res.Total, len(res.List))
				if err != nil {
					return nil, err
				}
				if !hasMore {
					break
				}
				page++
			}
		}
	}
	return out, nil
}

func (s *Service) buildGoodsRequests(ctx context.Context, provider shopprovider.Provider, starget shopprovider.Target, target *storage.ShopTarget, goodsType string) ([]shopprovider.GoodsRequest, error) {
	mode := target.ScopeMode
	if mode == "" {
		mode = storage.ShopScopeAll
	}
	base := shopprovider.GoodsRequest{GoodsType: goodsType, CategoryID: 0}
	switch mode {
	case storage.ShopScopeFilters:
		requests := make([]shopprovider.GoodsRequest, 0)
		for _, id := range parseInt64List(target.CategoryIDsJSON) {
			requests = append(requests, shopprovider.GoodsRequest{GoodsType: goodsType, CategoryID: id})
		}
		categoryNames := parseStringList(target.CategoryNamesJSON)
		if len(categoryNames) > 0 {
			categories, err := provider.Categories(ctx, starget, shopprovider.CategoryRequest{GoodsType: goodsType})
			if err != nil {
				return nil, fmt.Errorf("load shop categories: %w", err)
			}
			wanted := map[string]struct{}{}
			for _, name := range categoryNames {
				wanted[strings.ToLower(name)] = struct{}{}
			}
			for _, category := range categories {
				if _, ok := wanted[strings.ToLower(strings.TrimSpace(category.Name))]; ok {
					requests = append(requests, shopprovider.GoodsRequest{GoodsType: goodsType, CategoryID: category.ID})
				}
			}
		}
		for _, keyword := range parseStringList(target.KeywordsJSON) {
			requests = append(requests, shopprovider.GoodsRequest{GoodsType: goodsType, CategoryID: 0, Keywords: keyword})
		}
		if len(requests) > 0 {
			return requests, nil
		}
	case storage.ShopScopeGoodsKeys:
		keys := parseStringList(target.GoodsKeysJSON)
		if len(keys) > 0 {
			requests := make([]shopprovider.GoodsRequest, 0)
			for _, key := range keys {
				requests = append(requests, shopprovider.GoodsRequest{GoodsType: goodsType, CategoryID: 0, Keywords: key})
			}
			return requests, nil
		}
	}
	return []shopprovider.GoodsRequest{base}, nil
}

func (s *Service) fetchSingleGoods(ctx context.Context, provider shopprovider.Provider, starget shopprovider.Target, current *storage.ShopGoodsSnapshot, goodsKey string) (*shopprovider.Goods, bool, error) {
	goodsType := "card"
	categoryID := int64(0)
	if current != nil {
		if strings.TrimSpace(current.GoodsType) != "" {
			goodsType = current.GoodsType
		}
		categoryID = current.CategoryID
	}
	requests := []shopprovider.GoodsRequest{
		{GoodsType: goodsType, CategoryID: categoryID, Keywords: goodsKey},
	}
	if categoryID != 0 {
		requests = append(requests, shopprovider.GoodsRequest{GoodsType: goodsType, CategoryID: categoryID})
	}
	if categoryID != 0 {
		requests = append(requests, shopprovider.GoodsRequest{GoodsType: goodsType, CategoryID: 0, Keywords: goodsKey})
	}
	requests = append(requests, shopprovider.GoodsRequest{GoodsType: goodsType, CategoryID: 0})
	for _, req := range requests {
		for page := 1; ; page++ {
			req.Page = page
			req.PageSize = shopGoodsPageSize
			res, err := provider.Goods(ctx, starget, req)
			if err != nil {
				return nil, false, err
			}
			for _, item := range res.List {
				if item.GoodsKey == goodsKey {
					item := item
					return &item, true, nil
				}
			}
			hasMore, err := hasMoreShopGoodsPages(page, req.PageSize, res.Total, len(res.List))
			if err != nil {
				return nil, false, err
			}
			if !hasMore {
				break
			}
		}
	}
	return nil, false, nil
}

func hasMoreShopGoodsPages(page, pageSize, total, count int) (bool, error) {
	if count == 0 || count < pageSize {
		return false, nil
	}
	if total > 0 && page*pageSize >= total {
		return false, nil
	}
	if page >= maxShopGoodsPages {
		return false, fmt.Errorf("shop goods pagination exceeded %d pages", maxShopGoodsPages)
	}
	return true, nil
}

func (s *Service) diffAndSave(target *storage.ShopTarget, fetched map[string]shopprovider.Goods, now time.Time) ([]storage.ShopGoodsChangeLog, int, error) {
	existing, err := s.goods.ListByTarget(target.ID)
	if err != nil {
		return nil, 0, err
	}
	firstSync := len(existing) == 0
	existingByKey := make(map[string]storage.ShopGoodsSnapshot, len(existing))
	for _, snapshot := range existing {
		existingByKey[snapshot.GoodsKey] = snapshot
	}

	changes := make([]storage.ShopGoodsChangeLog, 0)
	lowStockCount := 0
	keys := make([]string, 0, len(fetched))
	for key := range fetched {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := map[string]struct{}{}
	for _, key := range keys {
		item := fetched[key]
		seen[key] = struct{}{}
		if target.StockThreshold > 0 && item.StockCount <= target.StockThreshold {
			lowStockCount++
		}
		prev, ok := existingByKey[key]
		if !ok {
			snapshot := snapshotFromGoods(target.ID, item, now)
			if err := s.goods.CreateSnapshot(&snapshot); err != nil {
				return nil, 0, err
			}
			if !firstSync && target.NewGoodsEnabled {
				if err := s.appendChange(&changes, target, item, storage.ShopChangeGoodsAdded, "", item.Name, fmt.Sprintf("新增商品 %s", item.Name), now); err != nil {
					return nil, 0, err
				}
			}
			continue
		}
		next := snapshotFromGoods(target.ID, item, now)
		next.ID = prev.ID
		next.FirstSeenAt = prev.FirstSeenAt
		changed := false
		drafts := make([]changeDraft, 0, 3)
		if prev.RemovedAt != nil {
			changed = true
			if target.RestockEnabled {
				drafts = append(drafts, changeDraft{item: item, event: storage.ShopChangeGoodsRestocked, oldValue: "removed", newValue: strconv.Itoa(item.StockCount), summary: fmt.Sprintf("%s 重新出现，当前库存 %d", item.Name, item.StockCount)})
			}
		}
		if target.PriceChangeEnabled && prev.Price != item.Price {
			changed = true
			drafts = append(drafts, changeDraft{item: item, event: storage.ShopChangePriceChanged, oldValue: fmtFloat(prev.Price), newValue: fmtFloat(item.Price), summary: fmt.Sprintf("%s 价格 %.4f -> %.4f", item.Name, prev.Price, item.Price)})
		}
		if target.StockChangeEnabled && prev.StockCount != item.StockCount {
			changed = true
			drafts = append(drafts, changeDraft{item: item, event: storage.ShopChangeStockChanged, oldValue: strconv.Itoa(prev.StockCount), newValue: strconv.Itoa(item.StockCount), summary: fmt.Sprintf("%s 库存 %d -> %d", item.Name, prev.StockCount, item.StockCount)})
		}
		if target.RestockEnabled && prev.RemovedAt == nil && prev.StockCount == 0 && item.StockCount > 0 {
			changed = true
			drafts = append(drafts, changeDraft{item: item, event: storage.ShopChangeGoodsRestocked, oldValue: "0", newValue: strconv.Itoa(item.StockCount), summary: fmt.Sprintf("%s 补货，当前库存 %d", item.Name, item.StockCount)})
		}
		if target.LowStockEnabled && target.StockThreshold > 0 && prev.StockCount > target.StockThreshold && item.StockCount <= target.StockThreshold {
			changed = true
			drafts = append(drafts, changeDraft{item: item, event: storage.ShopChangeStockLow, oldValue: strconv.Itoa(prev.StockCount), newValue: strconv.Itoa(item.StockCount), summary: fmt.Sprintf("%s 库存 %d，低于阈值 %d", item.Name, item.StockCount, target.StockThreshold)})
		}
		if changed {
			next.LastChangedAt = &now
		} else {
			next.LastChangedAt = prev.LastChangedAt
		}
		if err := s.goods.SaveSnapshot(&next); err != nil {
			return nil, 0, err
		}
		for _, draft := range drafts {
			if err := s.appendChange(&changes, target, draft.item, draft.event, draft.oldValue, draft.newValue, draft.summary, now); err != nil {
				return nil, 0, err
			}
		}
	}

	for _, prev := range existing {
		if _, ok := seen[prev.GoodsKey]; ok || prev.RemovedAt != nil {
			continue
		}
		removedAt := now
		prev.RemovedAt = &removedAt
		prev.LastChangedAt = &now
		if err := s.goods.SaveSnapshot(&prev); err != nil {
			return nil, 0, err
		}
		if target.RemovedGoodsEnabled {
			item := shopprovider.Goods{GoodsKey: prev.GoodsKey, Name: prev.Name}
			if err := s.appendChange(&changes, target, item, storage.ShopChangeGoodsRemoved, prev.Name, "", fmt.Sprintf("商品消失或下架: %s", prev.Name), now); err != nil {
				return nil, 0, err
			}
		}
	}
	return changes, lowStockCount, nil
}

func shopSnapshotChanged(prev, next storage.ShopGoodsSnapshot) bool {
	return prev.Name != next.Name ||
		prev.GoodsType != next.GoodsType ||
		prev.CategoryID != next.CategoryID ||
		prev.CategoryName != next.CategoryName ||
		prev.Link != next.Link ||
		prev.Price != next.Price ||
		prev.MarketPrice != next.MarketPrice ||
		prev.StockCount != next.StockCount ||
		prev.LimitCount != next.LimitCount ||
		prev.SendOrder != next.SendOrder ||
		prev.ContactFormat != next.ContactFormat
}

func (s *Service) appendChange(changes *[]storage.ShopGoodsChangeLog, target *storage.ShopTarget, item shopprovider.Goods, event storage.ShopGoodsChangeEvent, oldValue, newValue, summary string, changedAt time.Time) error {
	log := storage.ShopGoodsChangeLog{
		TargetID:  target.ID,
		GoodsKey:  item.GoodsKey,
		GoodsName: item.Name,
		Event:     event,
		OldValue:  oldValue,
		NewValue:  newValue,
		Summary:   summary,
		ChangedAt: changedAt,
	}
	if err := s.goods.AppendChange(&log); err != nil {
		return fmt.Errorf("记录店铺商品变化失败：%w", err)
	}
	*changes = append(*changes, log)
	return nil
}

func (s *Service) recordFailure(target *storage.ShopTarget, started time.Time, err error) {
	finished := time.Now()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if err := s.targets.SetSyncResult(target.ID, nil, msg, target.LastShopName, target.LastGoodsCount, target.LastLowStockGoods, 0); err != nil && s.log != nil {
		s.log.Warn("save shop sync failure result failed", "target_id", target.ID, "err", err)
	}
	if err := s.goods.AppendMonitorLog(&storage.ShopMonitorLog{
		TargetID:     target.ID,
		Success:      false,
		ErrorMessage: msg,
		StartedAt:    started,
		FinishedAt:   finished,
	}); err != nil && s.log != nil {
		s.log.Warn("append shop failure monitor log failed", "target_id", target.ID, "err", err)
	}
	if err := s.goods.AppendChange(&storage.ShopGoodsChangeLog{
		TargetID:  target.ID,
		Event:     storage.ShopChangeMonitorFailed,
		Summary:   msg,
		ChangedAt: finished,
	}); err != nil && s.log != nil {
		s.log.Warn("append shop failure change log failed", "target_id", target.ID, "err", err)
	}
}

func (s *Service) notifyFailure(ctx context.Context, target *storage.ShopTarget, err error) {
	if s.dispatcher == nil || err == nil || target == nil || !target.NotifyEnabled {
		return
	}
	if !s.hasMatchingWatchRule(target.ID, storage.ShopChangeMonitorFailed, nil, "", "") {
		return
	}
	if dispatchErr := s.dispatcher.Dispatch(ctx, notify.Message{
		Event:   storage.EventShopMonitorFailed,
		Subject: fmt.Sprintf("%s 店铺监控失败", target.Name),
		Body:    err.Error(),
	}); dispatchErr != nil && s.log != nil {
		s.log.Warn("dispatch shop monitor failure failed", "target_id", target.ID, "err", dispatchErr)
	}
}

func (s *Service) dispatchChanges(ctx context.Context, target *storage.ShopTarget, info *shopprovider.ShopInfo, changes []storage.ShopGoodsChangeLog) error {
	if s.dispatcher == nil || target == nil || !target.NotifyEnabled || len(changes) == 0 {
		return nil
	}
	changes = s.filterWatchRuleChanges(target.ID, changes)
	if len(changes) == 0 {
		return nil
	}
	counts := map[storage.ShopGoodsChangeEvent]int{}
	for _, change := range changes {
		counts[change.Event]++
	}
	title := target.Name
	if info != nil && strings.TrimSpace(info.Name) != "" {
		title = info.Name
	}
	grouped := map[storage.ShopGoodsChangeEvent][]string{}
	for _, change := range changes {
		if len(grouped[change.Event]) < 12 {
			grouped[change.Event] = append(grouped[change.Event], "- "+change.Summary)
		}
	}
	var firstErr error
	for _, event := range []storage.ShopGoodsChangeEvent{
		storage.ShopChangeGoodsAdded,
		storage.ShopChangeGoodsRemoved,
		storage.ShopChangePriceChanged,
		storage.ShopChangeStockChanged,
		storage.ShopChangeStockLow,
		storage.ShopChangeGoodsRestocked,
	} {
		eventLines := grouped[event]
		if len(eventLines) == 0 {
			continue
		}
		body := []string{fmt.Sprintf("%s %s: %d", title, shopEventLabel(event), counts[event]), ""}
		body = append(body, eventLines...)
		if err := s.dispatcher.Dispatch(ctx, notify.Message{
			Event:   notificationEventForShopChange(event),
			Subject: fmt.Sprintf("%s 店铺%s", title, shopEventLabel(event)),
			Body:    strings.Join(body, "\n"),
		}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) filterWatchRuleChanges(targetID uint, changes []storage.ShopGoodsChangeLog) []storage.ShopGoodsChangeLog {
	if s.watchRules == nil {
		return nil
	}
	rules, err := s.watchRules.ListEnabledByTarget(targetID)
	if err != nil || len(rules) == 0 {
		if err != nil && s.log != nil {
			s.log.Warn("list shop watch rules failed", "target_id", targetID, "err", err)
		}
		return nil
	}
	out := make([]storage.ShopGoodsChangeLog, 0, len(changes))
	snapshotCache := map[string]*storage.ShopGoodsSnapshot{}
	for _, change := range changes {
		var snapshot *storage.ShopGoodsSnapshot
		if strings.TrimSpace(change.GoodsKey) != "" {
			if cached, ok := snapshotCache[change.GoodsKey]; ok {
				snapshot = cached
			} else if s.goods != nil {
				found, err := s.goods.FindSnapshot(targetID, change.GoodsKey)
				if err == nil {
					snapshot = found
				}
				snapshotCache[change.GoodsKey] = snapshot
			}
		}
		for _, rule := range rules {
			if storage.ShopWatchRuleMatchesChange(rule, change.Event, snapshot, change.GoodsKey, change.GoodsName) {
				out = append(out, change)
				break
			}
		}
	}
	return out
}

func (s *Service) hasMatchingWatchRule(targetID uint, event storage.ShopGoodsChangeEvent, snapshot *storage.ShopGoodsSnapshot, goodsKey, goodsName string) bool {
	if s.watchRules == nil {
		return false
	}
	rules, err := s.watchRules.ListEnabledByTarget(targetID)
	if err != nil {
		if s.log != nil {
			s.log.Warn("list shop watch rules failed", "target_id", targetID, "err", err)
		}
		return false
	}
	for _, rule := range rules {
		if storage.ShopWatchRuleMatchesChange(rule, event, snapshot, goodsKey, goodsName) {
			return true
		}
	}
	return false
}

func snapshotFromGoods(targetID uint, item shopprovider.Goods, now time.Time) storage.ShopGoodsSnapshot {
	return storage.ShopGoodsSnapshot{
		TargetID:      targetID,
		GoodsKey:      item.GoodsKey,
		GoodsType:     item.GoodsType,
		Name:          item.Name,
		CategoryID:    item.CategoryID,
		CategoryName:  item.CategoryName,
		Link:          item.Link,
		Price:         item.Price,
		MarketPrice:   item.MarketPrice,
		StockCount:    item.StockCount,
		LimitCount:    item.LimitCount,
		SendOrder:     item.SendOrder,
		ContactFormat: item.ContactFormat,
		RawJSON:       item.RawJSON,
		FirstSeenAt:   now,
		LastSeenAt:    now,
		RemovedAt:     nil,
	}
}

func parseStringList(raw string) []string {
	var list []string
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func parseInt64List(raw string) []int64 {
	var list []int64
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	return list
}

func firstGoodsType(raw string) string {
	list := parseStringList(raw)
	if len(list) == 0 {
		return "card"
	}
	return list[0]
}

func fmtFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func shopEventLabel(event storage.ShopGoodsChangeEvent) string {
	switch event {
	case storage.ShopChangeGoodsAdded:
		return "新增商品"
	case storage.ShopChangeGoodsRemoved:
		return "商品消失"
	case storage.ShopChangePriceChanged:
		return "价格变化"
	case storage.ShopChangeStockChanged:
		return "库存变化"
	case storage.ShopChangeStockLow:
		return "低库存"
	case storage.ShopChangeGoodsRestocked:
		return "补货"
	default:
		return string(event)
	}
}

func notificationEventForShopChange(event storage.ShopGoodsChangeEvent) storage.NotificationEvent {
	switch event {
	case storage.ShopChangeGoodsAdded:
		return storage.EventShopGoodsAdded
	case storage.ShopChangeGoodsRemoved:
		return storage.EventShopGoodsRemoved
	case storage.ShopChangePriceChanged:
		return storage.EventShopPriceChanged
	case storage.ShopChangeStockChanged:
		return storage.EventShopStockChanged
	case storage.ShopChangeStockLow:
		return storage.EventShopStockLow
	case storage.ShopChangeGoodsRestocked:
		return storage.EventShopGoodsRestocked
	default:
		return storage.EventShopStockChanged
	}
}
