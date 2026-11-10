-- 0029 down: money arriving, removed. Its records are evidence of money that
-- moved: take a backup first (40-migrate.sh refuses a down without one).
BEGIN;
DROP VIEW wallet_exceptions;
DROP TABLE vfd_reconciliation_runs;
DROP TABLE vfd_inflows;
DROP FUNCTION vfd_inflow_admissible();
DROP TABLE vfd_notification_attempts;
DROP TABLE vfd_notifications;
COMMIT;
