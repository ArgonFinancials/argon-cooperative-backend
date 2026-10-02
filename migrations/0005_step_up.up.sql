-- =============================================================================
-- ARGON — 0005: THE PIN STEP-UP
--
-- Written against test/tier1/tier1_0005_test.go (reviewed; mutation-tested
-- with test/tier1/mutants_0005 before this file existed), and approved
-- 2026-10-02.
--
--   - A PIN proof is minted FOR one act kind, bound at mint, never at use: a
--     proof minted for an import does not satisfy a reversal or a PIN change.
--     This applies to every act, not only the new one.
--   - A proof is never minted and used in one transaction: the code path that
--     spends a proof cannot have just minted it. (The other half is by role:
--     only argon_auth can mint at all — grants.sql, proven by probe P11–P14.)
--   - A ledger batch is authorised by a 'ledger_batch' act of the batch's own
--     actor, written in the batch's own transaction; one act, one batch. Like
--     every attributable act (0003), that act spends the actor's fresh,
--     unused PIN proof.
--
-- Rows written before 0005 are not re-judged: pre-0005 proofs carry no act
-- kind (and are long stale), pre-0005 batches name no act. The rules apply
-- to every row written from now on.
-- =============================================================================

BEGIN;

-- ---- proofs: scoped at mint, and where they were minted ---------------------
ALTER TABLE auth_proofs ADD COLUMN act_kind TEXT;
ALTER TABLE auth_proofs ADD COLUMN mint_xid xid8 NOT NULL DEFAULT pg_current_xact_id();
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release',
  'signatory_reset_request', 'signatory_phone_request', 'signatory_approval'));

CREATE FUNCTION auth_proofs_scoped() RETURNS trigger AS $$
BEGIN
  IF NEW.method = 'pin' AND NEW.act_kind IS NULL THEN
    PERFORM auth_refuse('pin_proof_names_act',
      'a PIN proof is minted for one act kind; an unscoped proof would authorise anything');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER auth_proofs_scoped BEFORE INSERT ON auth_proofs
  FOR EACH ROW EXECUTE FUNCTION auth_proofs_scoped();

-- ---- acts: the proof is for THIS act, and was minted elsewhere ---------------
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch'));

-- Fires after 0003's credential_acts_admissible (trigger names sort), which
-- has already refused a proof that is missing, not the actor's, stale or spent.
CREATE FUNCTION credential_act_proof_scoped() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k TEXT;
  x xid8;
BEGIN
  SELECT act_kind, mint_xid INTO k, x FROM auth_proofs WHERE id = NEW.proof_id;
  IF k IS DISTINCT FROM NEW.kind THEN
    PERFORM auth_refuse('act_proof_for_this_act',
      format('this proof was minted for %s, not %s: the kind is bound when the PIN is entered', coalesce(k, 'no act'), NEW.kind));
  END IF;
  IF x = pg_current_xact_id() THEN
    PERFORM auth_refuse('proof_minted_elsewhere',
      'a proof cannot be minted and spent in one transaction: the path that spends it must not be able to mint it');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER credential_acts_proof_scoped BEFORE INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION credential_act_proof_scoped();

-- ---- batches: authorised by their own act ------------------------------------
ALTER TABLE ledger_batches ADD COLUMN authorised_by UUID REFERENCES credential_acts(id);
ALTER TABLE ledger_batches ADD UNIQUE (authorised_by);   -- one act, one batch: ledger_batches_authorised_by_key

CREATE FUNCTION ledger_batch_authorised() RETURNS trigger
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k   TEXT;
  who UUID;
  x   xid8;
BEGIN
  IF NEW.authorised_by IS NULL THEN
    PERFORM auth_refuse('batch_needs_act', 'a ledger batch is authorised by its actor''s PIN step-up: it names its act');
  END IF;
  SELECT kind, actor_user_id, act_xid INTO k, who, x FROM credential_acts WHERE id = NEW.authorised_by;
  IF k IS DISTINCT FROM 'ledger_batch' THEN
    PERFORM auth_refuse('batch_act_is_ledger_batch', format('a batch is authorised by a ledger_batch act, not %s', coalesce(k, 'nothing')));
  END IF;
  IF who IS DISTINCT FROM NEW.actor_user_id THEN
    PERFORM auth_refuse('batch_act_by_actor', 'a batch rests on its own actor''s act, not someone else''s');
  END IF;
  IF x IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('batch_act_same_transaction', 'the act and the batch are one event: the act is written in the batch''s transaction');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER ledger_batches_authorised BEFORE INSERT ON ledger_batches
  FOR EACH ROW EXECUTE FUNCTION ledger_batch_authorised();

COMMIT;
