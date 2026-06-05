// Package checks defines the Check interface and the test battery run against a
// proxy through its local SOCKS endpoint: latency (generate_204) and adaptive
// multi-stream speed (download/upload). New approval checks (reachability,
// geo-match, dns-leak) plug in here without refactoring. See T0.5 in PLAN.md.
package checks
