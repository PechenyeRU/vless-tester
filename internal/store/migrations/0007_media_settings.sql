-- Media-unlock check settings, editable from the admin UI (Phase 4). Disabled by
-- default; when enabled, workers probe each listed platform through the proxy.
INSERT INTO settings (key, value) VALUES
    ('media.enabled',   'false'),
    ('media.platforms', '["openai","gemini","claude","spotify","netflix","youtube"]')
ON CONFLICT (key) DO NOTHING;
