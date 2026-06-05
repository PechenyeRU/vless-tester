// Package engine orchestrates the single-node pipeline: ingest -> core ->
// checks -> store -> output. In Phase 0 the worker runs in-process; the same
// abstractions back the remote worker in Phase 1. See T0.8 in PLAN.md.
package engine
