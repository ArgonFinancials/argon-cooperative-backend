-- =============================================================================
-- ARGON — 0010: A SIGNATURE AND A REJECTION REST ON THE SIGNER'S OWN STEP-UP
--
-- Written against test/tier1/tier1_0010_test.go (reviewed; mutation-tested
-- with test/tier1/mutants_0010 before this file existed). Decided 2026-10-07:
-- signing and rejecting require a PIN step-up. A signature is the most
-- attributable act in the product.
--
-- As a batch rests on its actor's act (0005), each signature and each
-- rejection names a credential act that is:
--   - the person's OWN (the act's actor is the user behind the member record),
--   - of the right kind (approval_signature / approval_rejection),
--   - written in the SAME transaction, so the act and the signature are one
--     event, and
--   - used ONCE.
-- The act itself already spends the person's fresh, unused PIN proof minted
-- for that kind, in another transaction (0003, 0005).
--
-- Rows written before 0010 are not re-judged: earlier signatures and
-- rejections name no act. The rules apply to every row written from now on.
--
-- ORDER (TestU3 holds this): the triggers are named ..._step_up so they fire
-- AFTER every existing rule on these tables (BEFORE triggers fire in name
-- order). A member record with no person behind it can take no step-up, and
-- the refusal it gets must be 0002's "no person", not this one.
--
-- AS THE APP (findings 16 and 25): the functions are SECURITY DEFINER with a
-- pinned search_path and read only named columns, because argon_app may not
-- read credential_acts.new_pin_hash. grants.sql adds act_id to argon_app's
-- INSERT lists on both tables, and the probe (deploy/70-verify-tier1.sh)
-- signs and rejects as argon_app.
-- =============================================================================

BEGIN;

-- ---- the two new act kinds ----------------------------------------------------
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection'));

-- ---- signatures ---------------------------------------------------------------
ALTER TABLE signatory_approvals ADD COLUMN act_id UUID REFERENCES credential_acts(id);
ALTER TABLE signatory_approvals ADD CONSTRAINT signature_act_once UNIQUE (act_id);

CREATE FUNCTION signature_on_step_up() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k   TEXT;
  who UUID;
  x   xid8;
BEGIN
  IF NEW.act_id IS NULL THEN
    PERFORM auth_refuse('signature_needs_step_up', 'a signature rests on the signer''s own PIN step-up: it names its act');
  END IF;
  SELECT kind, actor_user_id, act_xid INTO k, who, x FROM credential_acts WHERE id = NEW.act_id;
  IF who IS DISTINCT FROM (SELECT user_id FROM members WHERE id = NEW.member_id) THEN
    PERFORM auth_refuse('signature_act_is_signers', 'a signature rests on the signer''s own step-up, not someone else''s');
  END IF;
  IF k IS DISTINCT FROM 'approval_signature' THEN
    PERFORM auth_refuse('signature_act_kind', format('a signature rests on an approval_signature act, not %s', coalesce(k, 'nothing')));
  END IF;
  IF x IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('signature_act_same_transaction',
      'the act and the signature are one event: the act is written in the signature''s transaction');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER signatory_approvals_step_up BEFORE INSERT ON signatory_approvals
  FOR EACH ROW EXECUTE FUNCTION signature_on_step_up();

-- ---- rejections ---------------------------------------------------------------
ALTER TABLE approval_rejections ADD COLUMN act_id UUID REFERENCES credential_acts(id);
ALTER TABLE approval_rejections ADD CONSTRAINT rejection_act_once UNIQUE (act_id);

CREATE FUNCTION rejection_on_step_up() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k   TEXT;
  who UUID;
  x   xid8;
BEGIN
  IF NEW.act_id IS NULL THEN
    PERFORM auth_refuse('rejection_needs_step_up', 'a rejection rests on the rejecter''s own PIN step-up: it names its act');
  END IF;
  SELECT kind, actor_user_id, act_xid INTO k, who, x FROM credential_acts WHERE id = NEW.act_id;
  IF who IS DISTINCT FROM (SELECT user_id FROM members WHERE id = NEW.member_id) THEN
    PERFORM auth_refuse('rejection_act_is_rejecters', 'a rejection rests on the rejecter''s own step-up, not someone else''s');
  END IF;
  IF k IS DISTINCT FROM 'approval_rejection' THEN
    PERFORM auth_refuse('rejection_act_kind', format('a rejection rests on an approval_rejection act, not %s', coalesce(k, 'nothing')));
  END IF;
  IF x IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('rejection_act_same_transaction',
      'the act and the rejection are one event: the act is written in the rejection''s transaction');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_rejections_step_up BEFORE INSERT ON approval_rejections
  FOR EACH ROW EXECUTE FUNCTION rejection_on_step_up();

COMMIT;
