DROP TABLE IF EXISTS ai_label_salts;
DROP TABLE IF EXISTS ai_labels;
ALTER TABLE incident_ai_analyses DROP COLUMN IF EXISTS decision_snapshot;
