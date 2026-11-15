package monitors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// RestoreRequest turns a version's snapshot into the update that re-applies
// it to a monitor whose live configuration is current. Only fields that
// differ are sent, so a restore writes (and resets) nothing it does not
// change; ok is false when the monitor already matches.
//
// The config goes in masked, so the write-only secret merge keeps every
// secret the monitor holds now: "***" keeps a stored value and an absent
// field keeps it too. A restore therefore never brings a secret back and
// never removes one. Secret map keys added since the version are submitted
// as "***" as well (keepCurrentSecretKeys), because a submitted secret map
// is edited per key and would otherwise lose them. Group members travel as
// config.monitor_ids, which the handler syncs into monitor_groups like any
// group update.
func RestoreRequest(snapshot models.MonitorConfigSnapshot, current *models.MonitorConfigSnapshot) (req *models.UpdateMonitorRequest, ok bool, err error) {
	req = &models.UpdateMonitorRequest{}
	if snapshot.Name != current.Name {
		name := snapshot.Name
		req.Name = &name
		ok = true
	}
	if snapshot.Type != current.Type {
		typ := snapshot.Type
		req.Type = &typ
		ok = true
	}

	isGroup := snapshot.Type == models.MonitorTypeGroup
	configDiffers := !bytes.Equal(snapshot.Config, current.Config) || snapshot.Type != current.Type
	membersDiffer := isGroup && !reflect.DeepEqual(snapshot.MemberIDs, current.MemberIDs)
	if configDiffers || membersDiffer {
		config := snapshot.Config
		if snapshot.Type == current.Type {
			if config, err = keepCurrentSecretKeys(string(current.Type), config, current.Config); err != nil {
				return nil, false, err
			}
		}
		if isGroup {
			if config, err = withGroupMembers(config, snapshot.MemberIDs); err != nil {
				return nil, false, err
			}
		}
		req.Config = config
		ok = true
	}

	if snapshot.IntervalSeconds != current.IntervalSeconds {
		interval := snapshot.IntervalSeconds
		req.IntervalSeconds = &interval
		ok = true
	}
	if snapshot.TimeoutSeconds != current.TimeoutSeconds {
		timeout := snapshot.TimeoutSeconds
		req.TimeoutSeconds = &timeout
		ok = true
	}
	if !reflect.DeepEqual(snapshot.Tags, current.Tags) {
		tags := append([]string{}, snapshot.Tags...)
		req.Tags = &tags
		ok = true
	}
	if snapshot.ConsecutiveFailuresThreshold != current.ConsecutiveFailuresThreshold {
		threshold := snapshot.ConsecutiveFailuresThreshold
		req.ConsecutiveFailuresThreshold = &threshold
		ok = true
	}
	modeDiffers := snapshot.NotificationMode != current.NotificationMode
	if modeDiffers {
		mode := snapshot.NotificationMode
		req.NotificationMode = &mode
		ok = true
	}
	if snapshot.NotificationMode == "custom" &&
		(modeDiffers || !reflect.DeepEqual(snapshot.NotificationChannels, current.NotificationChannels)) {
		// Non-nil even when empty: the update replaces the route set.
		req.NotificationChannels = make([]models.MonitorChannelAssignment, 0, len(snapshot.NotificationChannels))
		for _, channel := range snapshot.NotificationChannels {
			req.NotificationChannels = append(req.NotificationChannels, models.MonitorChannelAssignment{
				ChannelID:    channel.ChannelID,
				DelaySeconds: channel.DelaySeconds,
			})
		}
		ok = true
	}
	if snapshot.MemberAlertRollup != current.MemberAlertRollup {
		rollup := snapshot.MemberAlertRollup
		req.MemberAlertRollup = &rollup
		ok = true
	}
	if snapshot.DependencySuppression != current.DependencySuppression {
		suppression := snapshot.DependencySuppression
		req.DependencySuppression = &suppression
		ok = true
	}
	// The location set and its quorum go together: a new set re-clamps the
	// quorum, so it is always sent with one.
	if !reflect.DeepEqual(snapshot.LocationIDs, current.LocationIDs) {
		locations := append([]string{}, snapshot.LocationIDs...)
		req.LocationIDs = &locations
		quorum := snapshot.LocationQuorum
		req.LocationQuorum = &quorum
		ok = true
	} else if snapshot.LocationQuorum != current.LocationQuorum {
		quorum := snapshot.LocationQuorum
		req.LocationQuorum = &quorum
		ok = true
	}
	if !isGroup && !reflect.DeepEqual(snapshot.DependsOnIDs, current.DependsOnIDs) {
		deps := append([]string{}, snapshot.DependsOnIDs...)
		req.DependsOnIDs = &deps
		ok = true
	}
	return req, ok, nil
}

// withGroupMembers sets config.monitor_ids to the snapshot's members: the
// junction table, not the stored config, is a group's membership.
func withGroupMembers(config json.RawMessage, memberIDs []string) (json.RawMessage, error) {
	value, err := decodeJSONValue(config)
	if err != nil {
		return nil, fmt.Errorf("decode group config: %w", err)
	}
	fields, _ := value.(map[string]any)
	if fields == nil {
		fields = map[string]any{}
	}
	fields["monitor_ids"] = append([]string{}, memberIDs...)
	return json.Marshal(fields)
}

// keepCurrentSecretKeys adds to each secret map field the version's config
// carries (secrets.MonitorSecretMapFields) the keys the live config holds and
// the version lacks, valued "***", so the merge keeps them. A map field
// absent from the version needs nothing: an absent map is kept whole.
func keepCurrentSecretKeys(monitorType string, config, currentConfig json.RawMessage) (json.RawMessage, error) {
	mapFields := secrets.MonitorSecretMapFields[monitorType]
	if len(mapFields) == 0 || len(currentConfig) == 0 {
		return config, nil
	}
	current := decodeConfigMap(currentConfig)
	value, err := decodeJSONValue(config)
	if err != nil {
		return nil, fmt.Errorf("decode version config: %w", err)
	}
	fields, _ := value.(map[string]any)
	if fields == nil {
		return config, nil
	}
	changed := false
	for _, field := range mapFields {
		restored, ok := fields[field].(map[string]any)
		if !ok {
			continue
		}
		live, _ := current[field].(map[string]any)
		for key := range live {
			// The merge matches keys case-insensitively (header names).
			if _, found := lookupKeyFold(restored, key); !found {
				restored[key] = secrets.MaskedSecret
				changed = true
			}
		}
	}
	if !changed {
		return config, nil
	}
	return json.Marshal(fields)
}

func lookupKeyFold(values map[string]any, key string) (any, bool) {
	if value, ok := values[key]; ok {
		return value, true
	}
	for candidate, value := range values {
		if strings.EqualFold(candidate, key) {
			return value, true
		}
	}
	return nil, false
}

// SecretsToReenter names the secret fields a version had set that the live
// monitor no longer holds ("password", "secret_variables.token"): the merge
// drops a kept "***" with nothing stored behind it, so after a restore they
// are empty until entered again. Both configs are masked or stored; only the
// presence of a value is read.
func SecretsToReenter(snapshot models.MonitorConfigSnapshot, currentConfig json.RawMessage) []string {
	version := decodeConfigMap(snapshot.Config)
	current := decodeConfigMap(currentConfig)
	out := []string{}
	for _, field := range secrets.MonitorSecretFields[string(snapshot.Type)] {
		if value, _ := version[field].(string); value != secrets.MaskedSecret {
			continue
		}
		if value, _ := current[field].(string); value == "" {
			out = append(out, field)
		}
	}
	for _, field := range secrets.MonitorSecretMapFields[string(snapshot.Type)] {
		restored, _ := version[field].(map[string]any)
		live, _ := current[field].(map[string]any)
		for key, value := range restored {
			if value != secrets.MaskedSecret {
				continue
			}
			if liveValue, found := lookupKeyFold(live, key); !found || liveValue == "" {
				out = append(out, field+"."+key)
			}
		}
	}
	sort.Strings(out)
	return out
}
