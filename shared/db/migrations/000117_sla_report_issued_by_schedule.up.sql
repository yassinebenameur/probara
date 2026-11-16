-- Reports a report schedule issued at period close name it as their issuer.
ALTER TABLE sla_reports ADD COLUMN issued_by_schedule BOOLEAN NOT NULL DEFAULT FALSE;
