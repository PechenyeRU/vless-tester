-- Publish-time output filters: a node-name prefix, a cap on published servers,
-- and node-name regex include/exclude filters. Defaults are no-ops.
INSERT INTO settings (key, value) VALUES
    ('output.node_prefix', '""'),
    ('output.success_limit', '0'),
    ('filter.name_include', '""'),
    ('filter.name_exclude', '""')
ON CONFLICT (key) DO NOTHING;
