package store

import (
	"context"
	"fmt"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// ServerFilter narrows a ListServers query. The zero value lists everything.
// Worker scopes the "latest measurement" to a single vantage point (the per-
// worker debug view); empty considers every worker's runs. Search matches the
// host, seq name or country (case-insensitive substring). Sort/Desc choose the
// order column (whitelisted); Limit/Offset paginate.
type ServerFilter struct {
	Country  string
	MinSpeed float64
	Worker   string
	Search   string
	Sort     string
	Desc     bool
	Limit    int
	Offset   int
}

// serverSortColumns whitelists the orderable columns, mapping the API sort key to
// a safe SQL expression (the value is never user-controlled, so it cannot inject).
var serverSortColumns = map[string]string{
	"speed":    "l.dl_mbps",
	"latency":  "l.latency_ms",
	"country":  "s.country",
	"seq":      "s.seq_name",
	"host":     "s.host",
	"port":     "s.port",
	"protocol": "s.protocol",
	"status":   "l.status",
	"last_run": "l.run_at",
}

// orderClause builds a safe "ORDER BY <col> <dir> NULLS LAST, s.id" from the
// filter, defaulting to fastest-first when the sort key is unknown.
func (f ServerFilter) orderClause() string {
	col, ok := serverSortColumns[f.Sort]
	if !ok {
		return "l.dl_mbps DESC NULLS LAST, s.id"
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}
	return fmt.Sprintf("%s %s NULLS LAST, s.id", col, dir)
}

// ServerSummary is a server plus its latest measurement, for the dashboard list.
// The measurement fields are nil when the server has not been tested yet (or has
// no run from the filtered worker).
type ServerSummary struct {
	ID        int64           `json:"id"`
	Protocol  model.Protocol  `json:"protocol"`
	Host      string          `json:"host"`
	Port      int             `json:"port"`
	Country   string          `json:"country"`
	SeqName   string          `json:"seq_name"`
	LatencyMs *int            `json:"latency_ms,omitempty"`
	DlMbps    *float64        `json:"dl_mbps,omitempty"`
	UlMbps    *float64        `json:"ul_mbps,omitempty"`
	Status    model.RunStatus `json:"status,omitempty"`
	LastRun   *time.Time      `json:"last_run,omitempty"`
	Worker    string          `json:"worker,omitempty"`
}

// ListServers returns servers with their latest measurement, applying the
// filter. Results are ordered fastest-first (untested servers last). The Worker
// filter restricts the "latest" run to that worker's measurements, exposing the
// per-worker debug view described in DESIGN 6.
func (s *Store) ListServers(ctx context.Context, f ServerFilter) ([]ServerSummary, error) {
	limit := f.Limit
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	// latest picks one run per server (the most recent), optionally scoped to a
	// worker. The min-speed, country and search filters apply to that latest row.
	// The ORDER BY is whitelisted (orderClause), so it is safe to interpolate.
	q := fmt.Sprintf(`
		WITH latest AS (
			SELECT DISTINCT ON (server_id)
			       server_id, worker_id, latency_ms, dl_mbps, ul_mbps, status, run_at
			FROM test_runs
			WHERE ($3 = '' OR worker_id = $3)
			ORDER BY server_id, run_at DESC
		)
		SELECT s.id, s.protocol, s.host, s.port,
		       COALESCE(s.country, ''), COALESCE(s.seq_name, ''),
		       l.latency_ms, l.dl_mbps, l.ul_mbps,
		       COALESCE(l.status, ''), l.run_at, COALESCE(l.worker_id, '')
		FROM servers s
		LEFT JOIN latest l ON l.server_id = s.id
		WHERE ($1 = '' OR s.country = $1)
		  AND ($2 <= 0 OR l.dl_mbps >= $2)
		  AND ($5 = '' OR s.host ILIKE '%%' || $5 || '%%' OR s.seq_name ILIKE '%%' || $5 || '%%' OR s.country ILIKE '%%' || $5 || '%%')
		ORDER BY %s
		LIMIT $4 OFFSET $6`, f.orderClause())
	rows, err := s.pool.Query(ctx, q, f.Country, f.MinSpeed, f.Worker, limit, f.Search, offset)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()

	var out []ServerSummary
	for rows.Next() {
		var ss ServerSummary
		var proto, status string
		if err := rows.Scan(
			&ss.ID, &proto, &ss.Host, &ss.Port, &ss.Country, &ss.SeqName,
			&ss.LatencyMs, &ss.DlMbps, &ss.UlMbps, &status, &ss.LastRun, &ss.Worker,
		); err != nil {
			return nil, fmt.Errorf("scan server summary: %w", err)
		}
		ss.Protocol = model.Protocol(proto)
		ss.Status = model.RunStatus(status)
		out = append(out, ss)
	}
	return out, rows.Err()
}

// ListServersCount returns how many servers match the filter (country, min-speed
// and search), ignoring Limit/Offset. It powers the paginated server list's page
// count, applying the same latest-run join as ListServers.
func (s *Store) ListServersCount(ctx context.Context, f ServerFilter) (int, error) {
	const q = `
		WITH latest AS (
			SELECT DISTINCT ON (server_id) server_id, dl_mbps
			FROM test_runs
			WHERE ($3 = '' OR worker_id = $3)
			ORDER BY server_id, run_at DESC
		)
		SELECT count(*)
		FROM servers s
		LEFT JOIN latest l ON l.server_id = s.id
		WHERE ($1 = '' OR s.country = $1)
		  AND ($2 <= 0 OR l.dl_mbps >= $2)
		  AND ($4 = '' OR s.host ILIKE '%' || $4 || '%' OR s.seq_name ILIKE '%' || $4 || '%' OR s.country ILIKE '%' || $4 || '%')`
	var n int
	if err := s.pool.QueryRow(ctx, q, f.Country, f.MinSpeed, f.Worker, f.Search).Scan(&n); err != nil {
		return 0, fmt.Errorf("count servers: %w", err)
	}
	return n, nil
}

// RunRecord is one historical measurement of a server from one worker, for the
// per-server detail view.
type RunRecord struct {
	ID        int64           `json:"id"`
	WorkerID  string          `json:"worker_id"`
	Phase     model.JobPhase  `json:"phase"`
	RunAt     time.Time       `json:"run_at"`
	LatencyMs *int            `json:"latency_ms,omitempty"`
	DlMbps    *float64        `json:"dl_mbps,omitempty"`
	UlMbps    *float64        `json:"ul_mbps,omitempty"`
	Status    model.RunStatus `json:"status"`
	Error     string          `json:"error,omitempty"`
}

// ServerHistory returns a server's measurement history, newest first, one row
// per worker run. limit caps the rows (default 200).
func (s *Store) ServerHistory(ctx context.Context, serverID int64, limit int) ([]RunRecord, error) {
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	const q = `
		SELECT id, worker_id, phase, run_at, latency_ms, dl_mbps, ul_mbps, status, COALESCE(error, '')
		FROM test_runs
		WHERE server_id = $1
		ORDER BY run_at DESC
		LIMIT $2`
	rows, err := s.pool.Query(ctx, q, serverID, limit)
	if err != nil {
		return nil, fmt.Errorf("server history %d: %w", serverID, err)
	}
	defer rows.Close()

	var out []RunRecord
	for rows.Next() {
		var r RunRecord
		var phase, status string
		if err := rows.Scan(
			&r.ID, &r.WorkerID, &phase, &r.RunAt,
			&r.LatencyMs, &r.DlMbps, &r.UlMbps, &status, &r.Error,
		); err != nil {
			return nil, fmt.Errorf("scan run record: %w", err)
		}
		r.Phase = model.JobPhase(phase)
		r.Status = model.RunStatus(status)
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountryStat aggregates servers and measurements per country.
type CountryStat struct {
	Country  string   `json:"country"`
	Servers  int      `json:"servers"`
	Tested   int      `json:"tested"`
	MedianDl *float64 `json:"median_dl_mbps,omitempty"`
}

// WorkerStat aggregates a worker's reported runs.
type WorkerStat struct {
	WorkerID string     `json:"worker_id"`
	Runs     int        `json:"runs"`
	OK       int        `json:"ok"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

// Stats is the dashboard aggregate: totals plus per-country and per-worker
// breakdowns (DESIGN 6).
type Stats struct {
	Servers   int           `json:"servers"`
	Runs      int           `json:"runs"`
	ByCountry []CountryStat `json:"by_country"`
	ByWorker  []WorkerStat  `json:"by_worker"`
}

// Stats computes the dashboard aggregates in a few grouped queries.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM servers`).Scan(&st.Servers); err != nil {
		return st, fmt.Errorf("stats servers: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM test_runs`).Scan(&st.Runs); err != nil {
		return st, fmt.Errorf("stats runs: %w", err)
	}

	// Per country: server count, how many have at least one ok run, and the
	// median latest download across tested servers in that country.
	const byCountry = `
		WITH latest AS (
			SELECT DISTINCT ON (server_id) server_id, dl_mbps, status
			FROM test_runs ORDER BY server_id, run_at DESC
		)
		SELECT COALESCE(NULLIF(s.country, ''), '??') AS country,
		       count(*) AS servers,
		       count(l.server_id) FILTER (WHERE l.status = 'ok') AS tested,
		       percentile_cont(0.5) WITHIN GROUP (
		           ORDER BY l.dl_mbps) FILTER (WHERE l.status = 'ok') AS median_dl
		FROM servers s
		LEFT JOIN latest l ON l.server_id = s.id
		GROUP BY 1
		ORDER BY servers DESC, country`
	rows, err := s.pool.Query(ctx, byCountry)
	if err != nil {
		return st, fmt.Errorf("stats by country: %w", err)
	}
	for rows.Next() {
		var c CountryStat
		if err := rows.Scan(&c.Country, &c.Servers, &c.Tested, &c.MedianDl); err != nil {
			rows.Close()
			return st, fmt.Errorf("scan country stat: %w", err)
		}
		st.ByCountry = append(st.ByCountry, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	// Per worker: total runs, ok runs, and the worker's last heartbeat.
	const byWorker = `
		SELECT r.worker_id, count(*) AS runs,
		       count(*) FILTER (WHERE r.status = 'ok') AS ok,
		       max(w.last_seen) AS last_seen
		FROM test_runs r
		LEFT JOIN workers w ON w.id = r.worker_id
		GROUP BY r.worker_id
		ORDER BY runs DESC, r.worker_id`
	wrows, err := s.pool.Query(ctx, byWorker)
	if err != nil {
		return st, fmt.Errorf("stats by worker: %w", err)
	}
	defer wrows.Close()
	for wrows.Next() {
		var w WorkerStat
		if err := wrows.Scan(&w.WorkerID, &w.Runs, &w.OK, &w.LastSeen); err != nil {
			return st, fmt.Errorf("scan worker stat: %w", err)
		}
		st.ByWorker = append(st.ByWorker, w)
	}
	return st, wrows.Err()
}

// SetSourceEnabled toggles a source on or off, the admin's source-management
// hook (DESIGN 6). Disabled sources are skipped by the ingest cycle.
func (s *Store) SetSourceEnabled(ctx context.Context, id int64, enabled bool) error {
	if _, err := s.pool.Exec(ctx, `UPDATE sources SET enabled = $2 WHERE id = $1`, id, enabled); err != nil {
		return fmt.Errorf("set source %d enabled: %w", id, err)
	}
	return nil
}

// ListAllSources returns every source including disabled ones, for the admin
// view (ListSources hides disabled rows because the ingest path must skip them).
func (s *Store) ListAllSources(ctx context.Context) ([]model.Source, error) {
	const q = `SELECT id, kind, location, last_fetch, enabled FROM sources ORDER BY id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list all sources: %w", err)
	}
	defer rows.Close()

	var out []model.Source
	for rows.Next() {
		var src model.Source
		var kind string
		if err := rows.Scan(&src.ID, &kind, &src.Location, &src.LastFetch, &src.Enabled); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		src.Kind = model.SourceKind(kind)
		out = append(out, src)
	}
	return out, rows.Err()
}
