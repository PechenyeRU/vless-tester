// Package core drives the sing-box proxy engine: it renders a sing-box config
// (local SOCKS inbound routed to the server outbound) for a normalized Server
// and manages the lifecycle (spawn, readiness, teardown) of the process. The
// sing-box binary is resolved from an explicit path, SINGBOX_BIN, an embedded
// copy (single-file builds with -tags embed_singbox), or the PATH.
package core
