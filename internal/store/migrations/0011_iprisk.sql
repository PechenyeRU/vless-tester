-- iprisk.enabled toggles exit-IP reputation scoring. When on, workers probe the
-- proxy's exit IP against a reputation provider and report a 0-100 risk score as
-- an ip_risk check. Disabled by default; informational (never gates the funnel).
INSERT INTO settings (key, value) VALUES
    ('iprisk.enabled', 'false')
ON CONFLICT (key) DO NOTHING;
