package discovery

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	contract "github.com/yassinebenameur/probara/shared/discovery"
)

// UpdateRequest is the body of PATCH /discovery/integrations/{id}. Namespaces
// change as a delta, so concurrent editors (two tabs, an MCP agent) merge
// instead of overwriting each other: adding a namespace that is active or
// pending, or removing one that is neither, is a no-op.
type UpdateRequest struct {
	Name *string `json:"name,omitempty"`
	// LocationID: absent keeps the location, null clears it, a UUID sets it.
	LocationID       json.RawMessage `json:"location_id,omitempty"`
	AddNamespaces    []string        `json:"add_namespaces,omitempty"`
	RemoveNamespaces []string        `json:"remove_namespaces,omitempty"`
}

// UpdateChanges is what an Update actually changed (no-ops excluded).
type UpdateChanges struct {
	Added           []string
	Removed         []string
	NameChanged     bool
	LocationChanged bool
}

// Any reports whether the update changed anything.
func (c UpdateChanges) Any() bool {
	return len(c.Added) > 0 || len(c.Removed) > 0 || c.NameChanged || c.LocationChanged
}

// locationPatch decodes UpdateRequest.LocationID: set reports whether the
// field was present, id is nil when it was null.
func locationPatch(raw json.RawMessage) (id *uuid.UUID, set bool, err error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	var v uuid.UUID
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false, invalid("location_id must be a location ID or null")
	}
	return &v, true, nil
}

// Update renames an integration, changes its private location, and adds or
// removes namespaces, under the integration row lock that Snapshot, Approve
// and Link take. Added namespaces become pending: the collector scans them
// from its next cycle and they turn active at their first full scan. Removed
// ones leave the active or pending list at once; their inventory leaves
// coverage now and is deleted after MissingRetention unless re-added (links
// come back with it). The home namespace cannot be removed. The revision is
// not bumped, so an edit never conflicts with a snapshot in flight; a scan
// that raced a removal is refused with ConfigChanged instead.
func (s *Service) Update(ctx context.Context, tenant, id uuid.UUID, req UpdateRequest) (*Integration, UpdateChanges, error) {
	var changes UpdateChanges
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" || len(trimmed) > 100 {
			return nil, changes, invalid("name must be 1–100 characters")
		}
		req.Name = &trimmed
	}
	location, locationSet, err := locationPatch(req.LocationID)
	if err != nil {
		return nil, changes, err
	}
	if len(req.AddNamespaces) > MaxNamespaces || len(req.RemoveNamespaces) > MaxNamespaces {
		return nil, changes, invalid(fmt.Sprintf("an integration has at most %d namespaces", MaxNamespaces))
	}
	for _, ns := range append(slices.Clone(req.AddNamespaces), req.RemoveNamespaces...) {
		if !contract.ValidNamespace(ns) {
			return nil, changes, invalid("invalid namespace name")
		}
	}
	for _, ns := range req.AddNamespaces {
		if slices.Contains(req.RemoveNamespaces, ns) {
			return nil, changes, invalid(fmt.Sprintf("namespace %s cannot be both added and removed", ns))
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, changes, err
	}
	defer tx.Rollback()
	current, err := scanIntegration(tx.QueryRowContext(ctx, `SELECT `+integrationColumns+` FROM discovery_integrations WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, changes, ErrNotFound
	}
	if err != nil {
		return nil, changes, err
	}
	next := current
	for _, ns := range req.RemoveNamespaces {
		if ns == current.HomeNamespace {
			return nil, changes, invalid(fmt.Sprintf("namespace %s is where the collector runs and cannot be removed; connect a new integration to move the collector", ns))
		}
	}
	next.Namespaces = slices.DeleteFunc(slices.Clone(current.Namespaces), func(ns string) bool { return slices.Contains(req.RemoveNamespaces, ns) })
	next.PendingNamespaces = slices.DeleteFunc(slices.Clone(current.PendingNamespaces), func(ns string) bool { return slices.Contains(req.RemoveNamespaces, ns) })
	for _, ns := range req.RemoveNamespaces {
		if (slices.Contains(current.Namespaces, ns) || slices.Contains(current.PendingNamespaces, ns)) && !slices.Contains(changes.Removed, ns) {
			changes.Removed = append(changes.Removed, ns)
		}
	}
	for _, ns := range req.AddNamespaces {
		if !slices.Contains(next.Namespaces, ns) && !slices.Contains(next.PendingNamespaces, ns) {
			next.PendingNamespaces = append(next.PendingNamespaces, ns)
			changes.Added = append(changes.Added, ns)
		}
	}
	if len(next.Namespaces)+len(next.PendingNamespaces) > MaxNamespaces {
		return nil, changes, invalid(fmt.Sprintf("an integration has at most %d namespaces, active and pending together", MaxNamespaces))
	}
	next.PendingStatus = map[string]PendingStatus{}
	for ns, st := range current.PendingStatus {
		if slices.Contains(next.PendingNamespaces, ns) {
			next.PendingStatus[ns] = st
		}
	}
	if req.Name != nil && *req.Name != current.Name {
		next.Name = *req.Name
		changes.NameChanged = true
	}
	if locationSet && !sameLocation(location, current.LocationID) {
		if location != nil {
			var ok bool
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM locations WHERE id=$1 AND tenant_id=$2 AND enabled AND deleted_at IS NULL)`, location, tenant).Scan(&ok)
			if err != nil {
				return nil, changes, err
			}
			if !ok {
				return nil, changes, invalid("choose an enabled location in this workspace")
			}
		}
		next.LocationID = location
		changes.LocationChanged = true
	}
	if !changes.Any() {
		return &current, changes, nil
	}
	statusJSON, err := json.Marshal(next.PendingStatus)
	if err != nil {
		return nil, changes, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE discovery_integrations SET name=$3,location_id=$4,namespaces=$5,pending_namespaces=$6,pending_status=$7::jsonb WHERE tenant_id=$1 AND id=$2`,
		tenant, id, next.Name, next.LocationID, pq.Array(next.Namespaces), pq.Array(next.PendingNamespaces), string(statusJSON))
	if err != nil {
		var pe *pq.Error
		if errors.As(err, &pe) && pe.Code == "23505" {
			return nil, changes, invalid("an integration with this name already exists")
		}
		return nil, changes, err
	}
	if err = tx.Commit(); err != nil {
		return nil, changes, err
	}
	next.normalize()
	return &next, changes, nil
}

func sameLocation(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// NamespaceStatus is one configured namespace of an integration as GET
// /discovery/integrations/{id}/namespaces reports it.
type NamespaceStatus struct {
	Namespace string `json:"namespace"`
	// State is "active" or "pending".
	State string `json:"state"`
	// Home marks the namespace the collector runs in (always active).
	Home bool `json:"home"`
	// Reason and CheckedAt: a pending namespace's last skip, if any.
	Reason    string     `json:"reason,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	// Services counts the stored discovered services in the namespace
	// (present or missing), LinkedServices those linked to a live monitor.
	// Both count whatever the state, so a re-added namespace shows what it
	// would bring back, and an active namespace with 0 services stands out.
	Services       int `json:"services"`
	LinkedServices int `json:"linked_services"`
}

// NamespaceList is the response of GET /discovery/integrations/{id}/namespaces.
type NamespaceList struct {
	Items []NamespaceStatus `json:"items"`
}

// Namespaces lists an integration's active and pending namespaces with their
// discovered and linked service counts: home first, then the other active
// ones in stored order, then the pending ones.
func (s *Service) Namespaces(ctx context.Context, tenant, id uuid.UUID) (NamespaceList, error) {
	out := NamespaceList{Items: []NamespaceStatus{}}
	i, err := scanIntegration(s.db.QueryRowContext(ctx, `SELECT `+integrationColumns+` FROM discovery_integrations WHERE tenant_id=$1 AND id=$2`, tenant, id))
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	type counts struct{ services, linked int }
	byNamespace := map[string]counts{}
	rows, err := s.db.QueryContext(ctx, `SELECT s.namespace,count(*),
	   count(*) FILTER (WHERE EXISTS(SELECT 1 FROM discovery_monitor_links l JOIN monitors m ON m.id=l.monitor_id AND m.tenant_id=l.tenant_id AND m.deleted_at IS NULL WHERE l.service_id=s.id))
	   FROM discovered_services s
	   WHERE s.tenant_id=$1 AND s.integration_id=$2
	     AND (EXISTS(SELECT 1 FROM discovered_resources r WHERE r.service_id=s.id) OR EXISTS(SELECT 1 FROM discovery_monitor_links l WHERE l.service_id=s.id))
	   GROUP BY s.namespace`, tenant, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var ns string
		var c counts
		if err = rows.Scan(&ns, &c.services, &c.linked); err != nil {
			return out, err
		}
		byNamespace[ns] = c
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	add := func(ns, state string) {
		c := byNamespace[ns]
		item := NamespaceStatus{Namespace: ns, State: state, Home: ns == i.HomeNamespace, Services: c.services, LinkedServices: c.linked}
		if st, ok := i.PendingStatus[ns]; ok && state == "pending" {
			checked := st.CheckedAt
			item.Reason, item.CheckedAt = st.Reason, &checked
		}
		out.Items = append(out.Items, item)
	}
	if slices.Contains(i.Namespaces, i.HomeNamespace) {
		add(i.HomeNamespace, "active")
	}
	for _, ns := range i.Namespaces {
		if ns != i.HomeNamespace {
			add(ns, "active")
		}
	}
	for _, ns := range i.PendingNamespaces {
		add(ns, "pending")
	}
	return out, nil
}
