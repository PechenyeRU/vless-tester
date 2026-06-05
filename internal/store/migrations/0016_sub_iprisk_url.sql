-- sub.path: optional obfuscated path token for the public /sub endpoint (empty =
-- served at the bare /sub). iprisk.url: optional IP-risk provider URL override.
INSERT INTO settings (key, value) VALUES
    ('sub.path', '""'),
    ('iprisk.url', '""')
ON CONFLICT (key) DO NOTHING;
