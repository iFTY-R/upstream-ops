package upstreamcap

import (
	"testing"

	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestCapabilityItemsForSub2API(t *testing.T) {
	items := capabilityItemsFor(storage.ChannelTypeSub2API)
	if level := levelForItems(items); level != LevelFullControl {
		t.Fatalf("level = %s, want %s", level, LevelFullControl)
	}
	assertCapability(t, items, CapAPIKeys, true, true)
	assertCapability(t, items, CapAPIKeyGroups, true, true)
	assertCapability(t, items, CapSubscription, true, false)
	assertCapability(t, items, CapSubscriptionUsage, true, false)
	assertCapability(t, items, CapOpenAIProbe, true, true)
}

func TestCapabilityItemsForNewAPI(t *testing.T) {
	items := capabilityItemsFor(storage.ChannelTypeNewAPI)
	if level := levelForItems(items); level != LevelFullControl {
		t.Fatalf("level = %s, want %s", level, LevelFullControl)
	}
	if !hasUnsupportedOptional(items) {
		t.Fatalf("newapi should report unsupported optional capabilities")
	}
	assertCapability(t, items, CapAPIKeys, true, true)
	assertCapability(t, items, CapAPIKeyGroups, true, true)
	assertCapability(t, items, CapSubscription, false, false)
	assertCapability(t, items, CapSubscriptionUsage, false, false)
	assertCapability(t, items, CapOpenAIProbe, true, true)
}

func TestSupportsCapability(t *testing.T) {
	if !supportsCapability(storage.ChannelTypeNewAPI, CapAPIKeyUpdate) {
		t.Fatalf("newapi should support %s", CapAPIKeyUpdate)
	}
	if supportsCapability(storage.ChannelTypeNewAPI, CapSubscription) {
		t.Fatalf("newapi should not support %s", CapSubscription)
	}
	if supportsCapability(storage.ChannelType("other"), CapAPIKeyUpdate) {
		t.Fatalf("unknown channel should not support %s", CapAPIKeyUpdate)
	}
}

func TestCapabilityItemsForUnknownChannel(t *testing.T) {
	items := capabilityItemsFor(storage.ChannelType("other"))
	if level := levelForItems(items); level != LevelUnsupported {
		t.Fatalf("level = %s, want %s", level, LevelUnsupported)
	}
	assertCapability(t, items, CapAPIKeys, false, true)
}

func assertCapability(t *testing.T, items []CapabilityItem, key string, supported, required bool) {
	t.Helper()
	for _, item := range items {
		if item.Key != key {
			continue
		}
		if item.Supported != supported || item.Required != required {
			t.Fatalf("%s = supported %v required %v, want supported %v required %v", key, item.Supported, item.Required, supported, required)
		}
		return
	}
	t.Fatalf("capability %s not found in %#v", key, items)
}
