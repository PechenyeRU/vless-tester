// Package store is the PostgreSQL data access layer (pgx): schema migrations,
// CRUD for servers/workers/runs/checks/sources, the job queue (claim via FOR
// UPDATE SKIP LOCKED), and the settings key/value table. See T0.2 in PLAN.md.
package store
