ALTER TABLE check_results
DROP CONSTRAINT IF EXISTS check_results_result_source_check;

ALTER TABLE check_results
DROP COLUMN IF EXISTS result_source;
