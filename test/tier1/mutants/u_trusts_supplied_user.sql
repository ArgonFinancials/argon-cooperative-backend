-- DELIBERATELY WRONG. Unique per user, but user_id is whatever the caller supplied.
ALTER TABLE signatory_approvals ADD CONSTRAINT signatory_approvals_one_per_user UNIQUE (request_id, user_id);
