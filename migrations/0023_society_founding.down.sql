-- 0023 down: no founding function and no standard chart. Societies already
-- founded keep their accounts and log rows; they are data, not this migration.
BEGIN;
DROP FUNCTION operator_create_society(TEXT, TEXT, TEXT, TEXT, INT, INT, INT, TEXT, TEXT, TEXT, DATE, TEXT);
DROP TABLE standard_chart;
COMMIT;
