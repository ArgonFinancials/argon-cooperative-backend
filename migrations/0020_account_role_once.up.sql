-- =============================================================================
-- ARGON — 0020: ONE ACCOUNT PER ROLE IN A SOCIETY (except 'general')
--
-- Written against test/tier1/tier1_0020_test.go (onboarding piece A, approved
-- 2026-10-30; recorded as a finding of its own). Mutation-tested with
-- test/tier1/mutants_0020 before this file existed.
--
-- THE FINDING: two money paths disagreed about where money goes. The money
-- endpoints (internal/api/money.go) posted to the FIRST account by code when a
-- society had two accounts of a role; the opening-balance batch refused the
-- same chart. One path guessed. A role names where the code posts; two
-- accounts of one role is a chart the code cannot read without guessing, so the
-- database refuses it. 'general' accounts are the society's own, never posted
-- to by role, and may be many.
-- =============================================================================

BEGIN;

DO $$
DECLARE
  dup TEXT;
BEGIN
  SELECT string_agg(format('%s has %s %s accounts', s.code, n, role), '; ') INTO dup
    FROM (SELECT society_id, role, count(*) n FROM accounts WHERE role <> 'general' GROUP BY 1, 2 HAVING count(*) > 1) d
    JOIN societies s ON s.id = d.society_id;
  IF dup IS NOT NULL THEN
    RAISE EXCEPTION '0020: a chart already has two accounts of one role (%). Which one the money went to cannot be inferred: resolve each deliberately (relabel the unused one general) before applying 0020.', dup;
  END IF;
END $$;

CREATE UNIQUE INDEX account_role_once ON accounts (society_id, role) WHERE role <> 'general';

COMMIT;
