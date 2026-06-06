package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/whitedns/vless-tester/internal/model"
)

// ErrServerExists is returned when an edit would collide with another server's
// fingerprint (the same endpoint+credential already has a row).
var ErrServerExists = errors.New("store: another server with the same identity already exists")

// UpsertServer inserts a server or, when the fingerprint already exists, updates
// its last_seen and raw_uri. It returns the server ID. This is the dedup point:
// the same fingerprint always maps to one row.
func (s *Store) UpsertServer(ctx context.Context, srv model.Server) (int64, error) {
	const q = `
		INSERT INTO servers (fingerprint, raw_uri, protocol, host, port)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (fingerprint) DO UPDATE
		SET raw_uri = EXCLUDED.raw_uri, last_seen = now()
		RETURNING id`
	var id int64
	if err := s.pool.QueryRow(ctx, q,
		srv.Fingerprint, srv.RawURI, string(srv.Protocol), srv.Host, srv.Port,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert server: %w", err)
	}
	return id, nil
}

// GetServer loads a server by ID.
func (s *Store) GetServer(ctx context.Context, id int64) (model.Server, error) {
	const q = `
		SELECT id, fingerprint, raw_uri, protocol, host, port,
		       COALESCE(country, ''), COALESCE(seq_name, ''), first_seen, last_seen
		FROM servers WHERE id = $1`
	var srv model.Server
	var proto string
	if err := s.pool.QueryRow(ctx, q, id).Scan(
		&srv.ID, &srv.Fingerprint, &srv.RawURI, &proto, &srv.Host, &srv.Port,
		&srv.Country, &srv.SeqName, &srv.FirstSeen, &srv.LastSeen,
	); err != nil {
		return model.Server{}, fmt.Errorf("get server %d: %w", id, err)
	}
	srv.Protocol = model.Protocol(proto)
	return srv, nil
}

// UpdateServer replaces a server's connection fields (re-parsed from a new raw
// link) plus its country and seq_name overrides. The fingerprint is updated too,
// so editing the link re-points the dedup identity. A fingerprint that already
// belongs to another row surfaces as a unique-violation the caller maps to a
// conflict. ok is false when no row has the given id.
func (s *Store) UpdateServer(ctx context.Context, id int64, srv model.Server) (bool, error) {
	const q = `
		UPDATE servers
		SET fingerprint = $2, raw_uri = $3, protocol = $4, host = $5, port = $6,
		    country = NULLIF($7, ''), seq_name = NULLIF($8, ''), last_seen = now()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id,
		srv.Fingerprint, srv.RawURI, string(srv.Protocol), srv.Host, srv.Port,
		srv.Country, srv.SeqName,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return false, ErrServerExists
		}
		return false, fmt.Errorf("update server %d: %w", id, err)
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteServer removes a server by id. Dependent rows (jobs, test_runs, checks)
// cascade via foreign keys. ok is false when no row matched.
func (s *Store) DeleteServer(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM servers WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete server %d: %w", id, err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetServerGeo records the GeoIP country and stable sequence name for a server.
func (s *Store) SetServerGeo(ctx context.Context, id int64, country, seqName string) error {
	const q = `UPDATE servers SET country = $2, seq_name = $3 WHERE id = $1`
	if _, err := s.pool.Exec(ctx, q, id, country, seqName); err != nil {
		return fmt.Errorf("set server geo %d: %w", id, err)
	}
	return nil
}

// CountServers returns the number of stored servers.
func (s *Store) CountServers(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM servers`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count servers: %w", err)
	}
	return n, nil
}
