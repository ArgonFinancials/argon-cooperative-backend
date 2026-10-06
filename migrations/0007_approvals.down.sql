-- Restores 0006's shape. The status column comes back FILLED FROM THE DERIVED
-- STATUS, so going down does not reset every request to 'awaiting'. The
-- rejection rows, and the resubmission links, are lost: take a backup first
-- (40-migrate.sh does).
BEGIN;
DROP TRIGGER approval_requests_resubmission ON approval_requests;
DROP FUNCTION resubmission_admissible();
ALTER TABLE approval_requests ADD COLUMN state approval_state NOT NULL DEFAULT 'awaiting';
UPDATE approval_requests SET state = approval_status(id);
ALTER TABLE approval_requests DROP COLUMN resubmits_id;
DROP TRIGGER signatory_approvals_on_open_request ON signatory_approvals;
DROP FUNCTION signature_on_open_request();
DROP FUNCTION approval_status(UUID, TIMESTAMPTZ);
DROP TABLE approval_rejections;
DROP FUNCTION rejection_admissible();
DROP FUNCTION approval_rejections_immutable();
COMMIT;
