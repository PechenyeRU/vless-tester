package store

import (
	"context"
	"fmt"
)

// ServerResult is a server's latest successful measurements, drawn from the
// append-only test_runs history. Approval is computed from these values, so the
// quality gate can be changed and re-applied without re-running any test.
type ServerResult struct {
	ServerID  int64
	RawURI    string
	Country   string
	SeqName   string
	LatencyMs *int
	DlMbps    *float64
	UlMbps    *float64
}

// ServerResults returns, for every server with at least one successful speed
// run, its most recent successful latency and speed measurements. When batchID
// is non-nil only that batch is considered ("only the latest batch"); when nil
// the most recent result per server across the whole history is used (rolling).
// It reads only history; it never triggers a test.
func (s *Store) ServerResults(ctx context.Context, batchID *int64) ([]ServerResult, error) {
	// $1 is the optional batch filter: when NULL, all batches qualify.
	const q = `
		SELECT s.id, s.raw_uri, COALESCE(s.country, ''), COALESCE(s.seq_name, ''),
		       lat.latency_ms, sp.dl_mbps, sp.ul_mbps
		FROM servers s
		LEFT JOIN LATERAL (
			SELECT latency_ms FROM test_runs t
			WHERE t.server_id = s.id AND t.phase = 'latency' AND t.status = 'ok'
			  AND ($1::bigint IS NULL OR t.batch_id = $1)
			ORDER BY run_at DESC LIMIT 1
		) lat ON true
		LEFT JOIN LATERAL (
			SELECT dl_mbps, ul_mbps FROM test_runs t
			WHERE t.server_id = s.id AND t.phase = 'speed' AND t.status = 'ok'
			  AND ($1::bigint IS NULL OR t.batch_id = $1)
			ORDER BY run_at DESC LIMIT 1
		) sp ON true
		WHERE sp.dl_mbps IS NOT NULL
		ORDER BY sp.dl_mbps DESC`
	rows, err := s.pool.Query(ctx, q, batchID)
	if err != nil {
		return nil, fmt.Errorf("server results: %w", err)
	}
	defer rows.Close()

	var out []ServerResult
	for rows.Next() {
		var r ServerResult
		var dl, ul *float64
		if err := rows.Scan(&r.ServerID, &r.RawURI, &r.Country, &r.SeqName, &r.LatencyMs, &dl, &ul); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		r.DlMbps, r.UlMbps = dl, ul
		out = append(out, r)
	}
	return out, rows.Err()
}
