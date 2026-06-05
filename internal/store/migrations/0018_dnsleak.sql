-- dnsleak.enabled toggles the DNS-leak check: workers compare the DNS resolver's
-- country against the exit country and report a dns_leak check. Disabled by
-- default; informational (never gates the funnel).
INSERT INTO settings (key, value) VALUES
    ('dnsleak.enabled', 'false')
ON CONFLICT (key) DO NOTHING;
