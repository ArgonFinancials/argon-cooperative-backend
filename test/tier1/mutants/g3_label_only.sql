-- DELIBERATELY WRONG. Refuses the internal_auditor LABEL; the auditor signs as anything else.
ALTER TABLE signatory_approvals ADD CONSTRAINT signatory_not_auditor CHECK (post_at_signing <> 'internal_auditor');
