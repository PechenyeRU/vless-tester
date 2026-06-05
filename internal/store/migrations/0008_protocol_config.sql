-- Per-worker and global protocol restrictions (panel-managed). protocols.enabled
-- is the global allow-list (all protocols by default); unchecking one excludes it
-- from every check. worker_tokens.protocols is an optional per-worker allow-list
-- (NULL = all protocols), so a worker without UDP support can be limited to TCP
-- protocols only.
INSERT INTO settings (key, value) VALUES
    ('protocols.enabled', '["vless","vmess","trojan","ss","hysteria2","hysteria","tuic","anytls","socks"]')
ON CONFLICT (key) DO NOTHING;

ALTER TABLE worker_tokens ADD COLUMN protocols JSONB;
