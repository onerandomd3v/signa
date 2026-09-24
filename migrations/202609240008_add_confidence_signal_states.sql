-- +goose Up
ALTER TABLE incident_reports
    ADD COLUMN independence_state TEXT,
    ADD COLUMN coordination_state TEXT;

ALTER TABLE incident_reports
    ADD CONSTRAINT incident_reports_independence_state_check
    CHECK (independence_state IS NULL OR independence_state IN ('independence_supported', 'repetition_risk', 'mixed_signals', 'indeterminate')),
    ADD CONSTRAINT incident_reports_coordination_state_check
    CHECK (coordination_state IS NULL OR coordination_state IN ('possible_coordination', 'no_signal', 'indeterminate'));

-- +goose Down
ALTER TABLE incident_reports
    DROP CONSTRAINT IF EXISTS incident_reports_coordination_state_check,
    DROP CONSTRAINT IF EXISTS incident_reports_independence_state_check,
    DROP COLUMN IF EXISTS coordination_state,
    DROP COLUMN IF EXISTS independence_state;
