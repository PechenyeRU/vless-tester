package store

import (
	"context"
	"fmt"
)

// ApprovedServer is a server that cleared the approval gate: at least the
// required number of distinct workers measured it passing both the latency and
// speed thresholds. MedianDlMbps is the corroborated speed (median across the
// passing workers), robust to a single worker's over- or under-reporting.
type ApprovedServer struct {
	ServerID     int64
	Fingerprint  string
	RawURI       string
	Host         string
	Country      string
	SeqName      string
	Workers      int
	MedianDlMbps float64
}

// ApprovedServers applies the corroboration gate over the append-only history:
// it keeps servers that at least `required` distinct workers measured with
// latency_ms <= maxLatencyMs AND dl_mbps >= minDlMbps. When batchID is non-nil
// only that batch counts; otherwise the whole history is considered. Approval is
// a pure function of stored results, so changing the gate and re-running this
// (no proxy tests) is the re-gate path. The published speed is the per-worker
// median, sorted best-first.
func (s *Store) ApprovedServers(ctx context.Context, batchID *int64, minDlMbps float64, maxLatencyMs, required int) ([]ApprovedServer, error) {
	if required < 1 {
		required = 1
	}
	// per_worker collapses each (server, worker) to its latest passing run, so a
	// worker is counted once and contributes one value to the median.
	const q = `
		WITH per_worker AS (
			SELECT DISTINCT ON (server_id, worker_id)
			       server_id, worker_id, dl_mbps
			FROM test_runs
			WHERE status = 'ok'
			  AND latency_ms IS NOT NULL AND latency_ms <= $2
			  AND dl_mbps  IS NOT NULL AND dl_mbps  >= $3
			  AND ($1::bigint IS NULL OR batch_id = $1)
			ORDER BY server_id, worker_id, run_at DESC
		)
		SELECT s.id, s.fingerprint, s.raw_uri, s.host,
		       COALESCE(s.country, ''), COALESCE(s.seq_name, ''),
		       count(*) AS workers,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY pw.dl_mbps) AS median_dl
		FROM per_worker pw
		JOIN servers s ON s.id = pw.server_id
		GROUP BY s.id, s.fingerprint, s.raw_uri, s.host, s.country, s.seq_name
		HAVING count(*) >= $4
		ORDER BY median_dl DESC`
	rows, err := s.pool.Query(ctx, q, batchID, maxLatencyMs, minDlMbps, required)
	if err != nil {
		return nil, fmt.Errorf("approved servers: %w", err)
	}
	defer rows.Close()

	var out []ApprovedServer
	for rows.Next() {
		var a ApprovedServer
		if err := rows.Scan(&a.ServerID, &a.Fingerprint, &a.RawURI, &a.Host, &a.Country, &a.SeqName, &a.Workers, &a.MedianDlMbps); err != nil {
			return nil, fmt.Errorf("scan approved: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
