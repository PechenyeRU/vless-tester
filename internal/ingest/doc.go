// Package ingest fetches proxy sources (raw link files and base64 subscription
// URLs), parses share links for all supported protocols (vless, vmess,
// hysteria2, tuic, trojan, ss) into a normalized Server, and deduplicates them
// by a deterministic fingerprint. See T0.3 in PLAN.md.
package ingest
