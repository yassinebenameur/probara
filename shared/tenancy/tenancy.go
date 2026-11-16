// Package tenancy holds the workspace (tenant) lifecycle rules that every
// service enforces.
//
// Deleting a workspace is a soft delete: the API stamps tenants.deleted_at
// (migration 000116), and the scheduler's workspace purge later removes the
// data in bounded batches and finally the row. Between the two the workspace
// is gone for everyone: every credential lookup, public read and background
// loop adds LiveTenantSQL (or, where a missing row must stay distinguishable
// from a deleted one, DeletedTenantSQL) to the query that resolves or
// iterates the tenant. Put the check in that shared lookup, never in a
// handler after the fact.
package tenancy

// DefaultTenantID is the workspace the first-user bootstrap seeds and the
// default OIDC_JIT_DEFAULT_TENANT_ID. It can never be deleted.
const DefaultTenantID = "00000000-0000-0000-0000-000000000001"

// LiveTenantSQL is a SQL predicate that holds when the tenant whose ID is in
// column exists and is not soft-deleted.
//
// It reads deleted_at through to_jsonb rather than naming the column, so the
// same query also runs on a schema without it (a new binary ahead of the
// migrations job, which runs post-upgrade, when nothing is deleted yet):
// these lookups run on every request and every background tick and must not
// fail during that window. The tenants table is small, and the planner turns
// the EXISTS into a semi-join, so the row-to-JSON conversion happens once per
// tenant row, not once per row of the outer query.
func LiveTenantSQL(column string) string {
	return `EXISTS (SELECT 1 FROM tenants live_tenant WHERE live_tenant.id = ` + column +
		` AND to_jsonb(live_tenant) ->> 'deleted_at' IS NULL)`
}

// DeletedTenantSQL is a SQL predicate that holds when the tenant whose ID is
// in column exists and is soft-deleted. Unlike NOT LiveTenantSQL it is false
// for an unknown ID, for callers that keep their own handling of a tenant
// that does not exist. Same schema tolerance as LiveTenantSQL.
func DeletedTenantSQL(column string) string {
	return `EXISTS (SELECT 1 FROM tenants deleted_tenant WHERE deleted_tenant.id = ` + column +
		` AND to_jsonb(deleted_tenant) ->> 'deleted_at' IS NOT NULL)`
}
