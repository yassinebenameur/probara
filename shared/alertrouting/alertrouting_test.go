package alertrouting

import (
	"testing"

	"github.com/google/uuid"
)

func TestClassify(t *testing.T) {
	groupID := uuid.New()

	tests := []struct {
		name          string
		counts        Counts
		wantReachable bool
		wantSource    string
		wantReason    string
	}{
		{
			name:          "custom mode with an active channel is reachable",
			counts:        Counts{NotificationMode: "custom", OwnActive: 1, OwnAssigned: 1},
			wantReachable: true,
			wantSource:    SourceCustom,
		},
		{
			name:          "custom mode with no assignment reaches nobody",
			counts:        Counts{NotificationMode: "custom"},
			wantReachable: false,
			wantSource:    SourceCustom,
			wantReason:    ReasonNoCustomChannels,
		},
		{
			name:          "custom mode with only disabled channels reaches nobody",
			counts:        Counts{NotificationMode: "custom", OwnAssigned: 2},
			wantReachable: false,
			wantSource:    SourceCustom,
			wantReason:    ReasonCustomChannelsDisabled,
		},
		{
			name:          "default mode inherits reachable tenant defaults",
			counts:        Counts{NotificationMode: "default", OwnActive: 2, OwnAssigned: 3},
			wantReachable: true,
			wantSource:    SourceTenantDefault,
		},
		{
			name:          "default mode with no tenant defaults reaches nobody",
			counts:        Counts{NotificationMode: "default"},
			wantReachable: false,
			wantSource:    SourceTenantDefault,
			wantReason:    ReasonNoTenantDefaults,
		},
		{
			name:          "default mode with only disabled tenant defaults reaches nobody",
			counts:        Counts{NotificationMode: "default", OwnAssigned: 1},
			wantReachable: false,
			wantSource:    SourceTenantDefault,
			wantReason:    ReasonTenantDefaultsDisabled,
		},
		{
			// The member is suppressed, so its own channels are irrelevant —
			// only the rolling-up group's routing decides.
			name: "rolled-up member follows its group's routing",
			counts: Counts{
				NotificationMode: "custom",
				OwnActive:        5,
				OwnAssigned:      5,
				RollupGroupID:    &groupID,
				RollupGroupName:  "payments",
				RollupActive:     0,
				RollupAssigned:   0,
				RollupEnabled:    true,
			},
			wantReachable: false,
			wantSource:    SourceGroupRollup,
			wantReason:    ReasonGroupRollupUnrouted,
		},
		{
			name: "rolled-up member is reachable through its group",
			counts: Counts{
				NotificationMode: "default",
				RollupGroupID:    &groupID,
				RollupGroupName:  "payments",
				RollupActive:     1,
				RollupAssigned:   1,
				RollupEnabled:    true,
			},
			wantReachable: true,
			wantSource:    SourceGroupRollup,
		},
		{
			// A per_monitor group never opens its own alert, so it is not
			// itself unrouted; each member carries its own status.
			name:          "per_monitor group defers to its members",
			counts:        Counts{NotificationMode: "default", SelfSilentGroup: true},
			wantReachable: true,
			wantSource:    SourceMembers,
		},
		{
			// Roll-up suppression ignores the group's enabled flag, so a
			// paused group silences its members while never alerting itself.
			name: "paused roll-up group silences its members",
			counts: Counts{
				NotificationMode: "default",
				RollupGroupID:    &groupID,
				RollupGroupName:  "payments",
				RollupActive:     2,
				RollupAssigned:   2,
				RollupEnabled:    false,
			},
			wantReachable: false,
			wantSource:    SourceGroupRollup,
			wantReason:    ReasonGroupRollupPaused,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.counts)
			if got.Reachable != tt.wantReachable {
				t.Errorf("Reachable = %v, want %v", got.Reachable, tt.wantReachable)
			}
			if got.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSource)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if got.Reachable && got.Reason != "" {
				t.Errorf("a reachable monitor must carry no reason, got %q", got.Reason)
			}
		})
	}
}

func TestClassifyReportsRollupGroup(t *testing.T) {
	groupID := uuid.New()
	got := Classify(Counts{
		NotificationMode: "default",
		RollupGroupID:    &groupID,
		RollupGroupName:  "payments",
		RollupActive:     2,
		RollupAssigned:   3,
		RollupEnabled:    true,
	})
	if got.RollupGroupID == nil || *got.RollupGroupID != groupID {
		t.Fatalf("RollupGroupID = %v, want %v", got.RollupGroupID, groupID)
	}
	if got.RollupGroupName != "payments" {
		t.Errorf("RollupGroupName = %q, want %q", got.RollupGroupName, "payments")
	}
	if got.ActiveChannels != 2 || got.AssignedChannels != 3 {
		t.Errorf("channel counts = (%d, %d), want (2, 3)", got.ActiveChannels, got.AssignedChannels)
	}
}

func TestClassifyIgnoresOwnRoutingWhenRolledUp(t *testing.T) {
	groupID := uuid.New()
	got := Classify(Counts{
		NotificationMode: "custom",
		OwnActive:        4,
		OwnAssigned:      4,
		RollupGroupID:    &groupID,
		RollupActive:     1,
		RollupAssigned:   1,
		RollupEnabled:    true,
	})
	if got.ActiveChannels != 1 {
		t.Errorf("ActiveChannels = %d, want the group's 1 — a suppressed member's own channels never fire", got.ActiveChannels)
	}
}
