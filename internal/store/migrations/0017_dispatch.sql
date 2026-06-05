-- Per-cycle dispatch knobs: shuffle the server order before enqueueing, and cap
-- how many servers are tested per run (0 = unlimited).
INSERT INTO settings (key, value) VALUES
    ('dispatch.shuffle', 'false'),
    ('dispatch.max_probes', '0')
ON CONFLICT (key) DO NOTHING;
