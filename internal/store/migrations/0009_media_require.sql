-- media.require lists platforms a server must unlock to be worth a speed test.
-- Empty by default (no media gating). When set, workers run media before the
-- speed test and skip the expensive speed leg for nodes that fail the filter.
INSERT INTO settings (key, value) VALUES
    ('media.require', '[]')
ON CONFLICT (key) DO NOTHING;
