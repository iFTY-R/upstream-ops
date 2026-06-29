package notify

import (
	"testing"

	"github.com/bejix/upstream-ops/backend/storage"
)

func TestSubscriptionMatchesLegacyAllEvents(t *testing.T) {
	sub := Subscription{
		ChannelID: 1,
		Mode:      SubscriptionModeGroups,
		Groups:    []string{"beta"},
	}

	if !sub.Matches(Message{ChannelID: 1, Event: storage.EventAnnouncement}) {
		t.Fatal("legacy subscription should match non-rate events")
	}
	if !sub.Matches(Message{ChannelID: 1, Event: storage.EventRateChanged, ModelName: "beta"}) {
		t.Fatal("legacy subscription should match selected rate group")
	}
	if sub.Matches(Message{ChannelID: 1, Event: storage.EventRateChanged, ModelName: "gamma"}) {
		t.Fatal("legacy subscription should reject unselected rate group")
	}
}

func TestSubscriptionMatchesSpecifiedEvents(t *testing.T) {
	sub := Subscription{
		ChannelID: 1,
		Mode:      SubscriptionModeAll,
		Events: []storage.NotificationEvent{
			storage.EventAnnouncement,
			storage.EventBalanceLow,
		},
	}

	if !sub.Matches(Message{ChannelID: 1, Event: storage.EventAnnouncement}) {
		t.Fatal("subscription should match selected announcement event")
	}
	if !sub.Matches(Message{ChannelID: 1, Event: storage.EventBalanceLow}) {
		t.Fatal("subscription should match selected balance event")
	}
	if sub.Matches(Message{ChannelID: 1, Event: storage.EventMonitorFailed}) {
		t.Fatal("subscription should reject unselected event")
	}
	if sub.Matches(Message{ChannelID: 2, Event: storage.EventAnnouncement}) {
		t.Fatal("subscription should reject another channel")
	}
}

func TestSubscriptionMatchesSpecifiedEventsAndGroups(t *testing.T) {
	sub := Subscription{
		ChannelID: 1,
		Mode:      SubscriptionModeGroups,
		Groups:    []string{"beta"},
		Events: []storage.NotificationEvent{
			storage.EventRateChanged,
			storage.EventSubscriptionExpiring,
		},
	}

	if !sub.Matches(Message{ChannelID: 1, Event: storage.EventRateChanged, ModelName: "beta"}) {
		t.Fatal("subscription should match selected rate event and group")
	}
	if sub.Matches(Message{ChannelID: 1, Event: storage.EventRateChanged, ModelName: "gamma"}) {
		t.Fatal("subscription should reject selected rate event with unselected group")
	}
	if !sub.Matches(Message{ChannelID: 1, Event: storage.EventSubscriptionExpiring}) {
		t.Fatal("subscription should match selected non-rate event without group")
	}
	if sub.Matches(Message{ChannelID: 1, Event: storage.EventAnnouncement}) {
		t.Fatal("subscription should reject unselected non-rate event")
	}
}
