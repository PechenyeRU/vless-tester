-- funnel.stages is the ordered, gateable pipeline the worker runs after the
-- latency gate. Each entry is {check, gate}; a gated stage that does not pass
-- skips the remaining stages for that node. The default preserves prior
-- behaviour: media gates (honouring media.require), ip_risk and speed do not.
INSERT INTO settings (key, value) VALUES
    ('funnel.stages', '[{"check":"media","gate":true},{"check":"ip_risk","gate":false},{"check":"speed","gate":false}]')
ON CONFLICT (key) DO NOTHING;
