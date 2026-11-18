// Package tenancy holds the workspace (tenant) lifecycle rules that every
// service enforces.
//
// Deleting a workspace is a soft delete: the API stamps tenants.deleted_at
// (migration 000115), and the scheduler's workspace purge later removes the
// data in bounded batches and finally the row. Between the two the workspace
// is gone for everyone: every credential lookup, public read and background
// loop adds LiveTenantSQL (or, where a missing row must stay distinguishable
// from a deleted one, DeletedTenantSQL) to the query that resolves or
// iterates the tenant. Put the check in that shared lookup, never in a
// handler after the fact.
//
// The fragments name tenants.deleted_at directly, so the planner has the
// column's statistics (an expression over the row would be estimated blind
// and turn the hot loops into per-tenant nested loops). They therefore need
// migration 000115 to have run before the binaries that use them serve: the
// documented upgrade order (migrations first) for this release.
package tenancy

// DefaultTenantID is the workspace the first-user bootstrap seeds and the
// default OIDC_JIT_DEFAULT_TENANT_ID. It can never be deleted.
const DefaultTenantID = "00000000-0000-0000-0000-000000000001"

// LiveTenantSQL is a SQL predicate that holds when the tenant whose ID is in
// column exists and is not soft-deleted. The planner turns the EXISTS into a
// semi-join against the (small) tenants table.
func LiveTenantSQL(column string) string {
	return `EXISTS (SELECT 1 FROM tenants live_tenant WHERE live_tenant.id = ` + column +
		` AND live_tenant.deleted_at IS NULL)`
}

// DeletedTenantSQL is a SQL predicate that holds when the tenant whose ID is
// in column exists and is soft-deleted. Unlike NOT LiveTenantSQL it is false
// for an unknown ID, for callers that keep their own handling of a tenant
// that does not exist.
func DeletedTenantSQL(column string) string {
	return `EXISTS (SELECT 1 FROM tenants deleted_tenant WHERE deleted_tenant.id = ` + column +
		` AND deleted_tenant.deleted_at IS NOT NULL)`
}
