-- Custom speed-test endpoints and sizing pushed to workers. Empty URLs keep the
-- worker default (Cloudflare); download_mb > 0 overrides the byte target.
INSERT INTO settings (key, value) VALUES
    ('speed.download_url', '""'),
    ('speed.upload_url', '""'),
    ('speed.timeout_ms', '30000'),
    ('speed.download_mb', '0')
ON CONFLICT (key) DO NOTHING;
