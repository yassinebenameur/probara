ALTER TABLE check_results
ADD COLUMN result_source TEXT NOT NULL DEFAULT 'monitor';

ALTER TABLE check_results
ADD CONSTRAINT check_results_result_source_check
CHECK (result_source IN ('monitor', 'platform'));

UPDATE check_results
SET result_source = 'platform'
WHERE status = 'error'
  AND error_message = 'Job expired before processing';
