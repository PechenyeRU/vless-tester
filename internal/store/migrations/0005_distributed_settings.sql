-- Settings for the distributed cycle, editable from the admin UI (Phase 2).
INSERT INTO settings (key, value) VALUES
    ('approval.required_workers', '1'),      -- distinct workers needed to approve
    ('approval.allow_partial',    'true'),   -- approve with fewer than N when the fleet is small
    ('jobs.lease_ttl',            '"2m"'),   -- a claim older than this is requeued
    ('jobs.max_attempts',         '3'),      -- requeues before a job is failed
    ('dispatch.interval',         '"12h"'),  -- how often a new cycle is dispatched
    ('reconcile.interval',        '"10s"')   -- how often the cycle is advanced
ON CONFLICT (key) DO NOTHING;
