-- End-of-cycle notifications. notify.urls is a list of shoutrrr service URLs
-- (telegram://, discord://, slack://, generic:// webhook, …); when notify.enabled
-- is true the coordinator sends a per-country summary after each published cycle.
INSERT INTO settings (key, value) VALUES
    ('notify.enabled', 'false'),
    ('notify.urls', '[]')
ON CONFLICT (key) DO NOTHING;
