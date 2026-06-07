-- sources.refresh is vestigial: the original design refreshed subscription
-- sources on their own cadence, but the implementation re-fetches every source
-- inside the dispatch cycle (dispatch.interval), so nothing ever read this
-- setting. Drop the dead seed to avoid implying a knob that does nothing.
DELETE FROM settings WHERE key = 'sources.refresh';
