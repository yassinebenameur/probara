package monitors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// ErrRestoreTypeChanged refuses restoring a version whose monitor type
// differs from the monitor's current one.
var ErrRestoreTypeChanged = errors.New("a restore never changes the monitor type")

// RestorePlan is the update that re-applies a version, and what it cannot
// bring back.
type RestorePlan struct {
	Request *models.UpdateMonitorRequest
	// Changed is false when the monitor already matches the version.
	Changed bool
	// SecretsToReenter are secrets the version had set that the monitor no
	// longer holds ("password", "secret_variables.token",
	// "headers.X-Api-Key"); they stay empty after the restore.
	SecretsToReenter []string
}

// PlanRestore builds the restore of a version onto the monitor, whose live
// configuration is current. See planRestore.
func (s *Service) PlanRestore(ctx context.Context, tenantID, monitorID uuid.UUID, version models.MonitorConfigSnapshot, current *models.MonitorConfigSnapshot) (*RestorePlan, error) {
	stored, err := s.repo.GetByID(ctx, tenantID, monitorID)
	if err != nil {
		return nil, err
	}
	return planRestore(version, current, stored.Config)
}

// planRestore turns a version's snapshot into the update that re-applies it.
// Only settings that differ from current are sent, so a restore writes (and
// resets) nothing it does not change.
//
// No secret value coming from a version is ever written: versions hold none
// by construction (MaskStoredMonitorConfig), and restoreConfig enforces it
// again, so a restore keeps every secret the monitor holds now and never
// brings one back or removes one. Group members travel as config.monitor_ids,
// which the handler syncs into monitor_groups like any group update.
// stored is the live monitor's stored config, read server-side only.
func planRestore(version models.MonitorConfigSnapshot, current *models.MonitorConfigSnapshot, stored json.RawMessage) (*RestorePlan, error) {
	plan := &RestorePlan{Request: &models.UpdateMonitorRequest{}, SecretsToReenter: []string{}}
	req := plan.Request
	for _, c := range snapshotColumns {
		if c.restore(&version, current, req) {
			plan.Changed = true
		}
	}
	// A version of another type is refused before planning (the handler):
	// its secrets would resolve against the current type's stored ones.
	if version.Type != current.Type {
		return nil, ErrRestoreTypeChanged
	}

	isGroup := version.Type == models.MonitorTypeGroup
	configDiffers := !bytes.Equal(version.Config, current.Config)
	membersDiffer := isGroup && !reflect.DeepEqual(version.MemberIDs, current.MemberIDs)
	if configDiffers || membersDiffer {
		config, reenter, err := restoreConfig(string(version.Type), version.Config, stored)
		if err != nil {
			return nil, err
		}
		if isGroup {
			if config, err = withGroupMembers(config, version.MemberIDs); err != nil {
				return nil, err
			}
		}
		req.Config = config
		plan.SecretsToReenter = reenter
		plan.Changed = true
	}

	if !reflect.DeepEqual(version.Tags, current.Tags) {
		tags := append([]string{}, version.Tags...)
		req.Tags = &tags
		plan.Changed = true
	}
	modeDiffers := version.NotificationMode != current.NotificationMode
	if modeDiffers {
		mode := version.NotificationMode
		req.NotificationMode = &mode
		plan.Changed = true
	}
	if version.NotificationMode == "custom" &&
		(modeDiffers || !reflect.DeepEqual(version.NotificationChannels, current.NotificationChannels)) {
		// Non-nil even when empty: the update replaces the route set.
		req.NotificationChannels = make([]models.MonitorChannelAssignment, 0, len(version.NotificationChannels))
		for _, channel := range version.NotificationChannels {
			req.NotificationChannels = append(req.NotificationChannels, models.MonitorChannelAssignment{
				ChannelID:    channel.ChannelID,
				DelaySeconds: channel.DelaySeconds,
			})
		}
		plan.Changed = true
	}
	// The location set and its quorum go together: a new set re-clamps the
	// quorum, so it is always sent with one.
	if !reflect.DeepEqual(version.LocationIDs, current.LocationIDs) {
		locations := append([]string{}, version.LocationIDs...)
		req.LocationIDs = &locations
		quorum := version.LocationQuorum
		req.LocationQuorum = &quorum
		plan.Changed = true
	} else if version.LocationQuorum != current.LocationQuorum {
		quorum := version.LocationQuorum
		req.LocationQuorum = &quorum
		plan.Changed = true
	}
	if !isGroup && !reflect.DeepEqual(version.DependsOnIDs, current.DependsOnIDs) {
		deps := append([]string{}, version.DependsOnIDs...)
		req.DependsOnIDs = &deps
		plan.Changed = true
	}
	return plan, nil
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

// plainSecretTwin is the secret map a top-level plain credential map moves
// into on a save (secrets.NormalizeSensitiveHeaders).
var plainSecretTwin = map[string]string{"headers": "secret_headers", "metadata": "secret_metadata", "variables": "secret_variables"}

// restoreConfig prepares a version's config so the save path keeps every
// secret stored now and writes no secret value from the version, whatever
// the version holds (all through the secret registry and the credential
// name rules, no per-type code):
//
//   - a registry secret field in the version is sent as "***": the stored
//     value is kept, and nothing is written when none is stored ("" would
//     clear it, a literal would replace it);
//   - every value of a registry secret map is sent as "***", and the keys
//     stored now that the version lacks are added as "***", because a
//     submitted secret map is edited per key;
//   - a plain credential entry (a credential-named header, metadata key or
//     variable) takes the value stored now under the same name, or is left
//     out: moving it on save would replace a stored secret;
//   - a miscased spelling of a secret key is dropped;
//   - a URL whose credentials the version masked (a userinfo password, a
//     credential-named query parameter) takes the stored URL when that is
//     the same URL masked, and otherwise loses the masked parts.
//
// It also returns the secrets the version had that the monitor no longer
// holds, which the restore leaves empty.
func restoreConfig(monitorType string, versionConfig, stored json.RawMessage) (json.RawMessage, []string, error) {
	value, err := decodeJSONValue(versionConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("decode version config: %w", err)
	}
	cfg, _ := value.(map[string]any)
	if cfg == nil {
		return versionConfig, []string{}, nil
	}
	storedCfg := decodeConfigMap(stored)
	reenter := map[string]struct{}{}
	set := func(v any) bool { return v != nil && v != "" }

	fields := secrets.MonitorSecretFields[monitorType]
	mapFields := secrets.MonitorSecretMapFields[monitorType]
	for key := range cfg {
		for _, field := range append(append([]string{}, fields...), mapFields...) {
			if key != field && strings.EqualFold(key, field) {
				delete(cfg, key)
			}
		}
	}
	for _, field := range fields {
		v, present := cfg[field]
		if !present {
			continue
		}
		if set(v) && !set(storedCfg[field]) {
			reenter[field] = struct{}{}
		}
		cfg[field] = secrets.MaskedSecret
	}
	for _, field := range mapFields {
		v, present := cfg[field]
		if !present {
			continue
		}
		entries, ok := v.(map[string]any)
		if !ok {
			delete(cfg, field)
			continue
		}
		live, _ := storedCfg[field].(map[string]any)
		for key, entry := range entries {
			if storedEntry, found := lookupKeyFold(live, key); set(entry) && (!found || !set(storedEntry)) {
				reenter[field+"."+key] = struct{}{}
			}
			entries[key] = secrets.MaskedSecret
		}
		for key := range live {
			if _, found := lookupKeyFold(entries, key); !found {
				entries[key] = secrets.MaskedSecret
			}
		}
	}

	storedPlain := map[string]any{}
	secrets.WalkPlainCredentials(monitorType, storedCfg, func(where string, entries map[string]any, name string) {
		storedPlain[strings.ToLower(where+"\x00"+name)] = entries[name]
	})
	secrets.WalkPlainCredentials(monitorType, cfg, func(where string, entries map[string]any, name string) {
		if live, found := storedPlain[strings.ToLower(where+"\x00"+name)]; found {
			entries[name] = live
			return
		}
		delete(entries, name)
		if twin, ok := plainSecretTwin[strings.ToLower(where)]; ok {
			twinEntries, _ := storedCfg[twin].(map[string]any)
			if storedEntry, found := lookupKeyFold(twinEntries, name); found && set(storedEntry) {
				return
			}
		}
		reenter[where+"."+name] = struct{}{}
	})

	restoreURLs(monitorType, cfg, storedCfg, reenter)

	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, err
	}
	list := make([]string, 0, len(reenter))
	for path := range reenter {
		list = append(list, path)
	}
	sort.Strings(list)
	return out, list, nil
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

// restoreURLs replaces each URL field (secrets.URLFields) whose
// credentials the snapshot masked: with the stored value at the same place
// when that masks to the same URL (the credential kept), otherwise with the
// URL without the masked parts, which is reported to enter again. Other
// fields are free text and restored as recorded.
func restoreURLs(monitorType string, cfg, stored map[string]any, reenter map[string]struct{}) {
	storedURLs := map[string]string{}
	secrets.WalkURLFields(monitorType, stored, func(where string, parent map[string]any, key string) {
		storedURLs[where] = parent[key].(string)
	})
	secrets.WalkURLFields(monitorType, cfg, func(where string, parent map[string]any, key string) {
		value := parent[key].(string)
		stripped, masked := secrets.StripMaskedURLCredentials(value)
		if !masked {
			return
		}
		if storedURL, ok := storedURLs[where]; ok {
			if remasked, _ := secrets.MaskURLCredentials(storedURL); remasked == value {
				parent[key] = storedURL
				return
			}
		}
		parent[key] = stripped
		reenter[where+" (URL credentials)"] = struct{}{}
	})
}
