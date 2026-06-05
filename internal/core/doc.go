// Package core drives the sing-box proxy engine: it renders a sing-box config
// (local SOCKS inbound routed to the server outbound) for a normalized Server
// and manages the lifecycle (spawn, readiness, teardown) of the process. The
// sing-box binary is embedded via go:embed in Phase 3. See T0.4 in PLAN.md.
package core
