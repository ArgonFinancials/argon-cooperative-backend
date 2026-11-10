-- DELIBERATELY WRONG (m_branch_guessed). A full copy of 0031 with one rule broken,
-- applied over 0031_interface.sql, whose objects are removed first.
BEGIN;
DROP FUNCTION register_member(UUID, TEXT, TEXT, TEXT, TEXT, DATE, collection_channel, TEXT, TEXT, TEXT, UUID, UUID, INT);
ALTER TABLE members DROP COLUMN register_line, DROP COLUMN register_batch_id, DROP COLUMN registered_by_act, DROP COLUMN next_of_kin_relationship, DROP COLUMN next_of_kin_phone, DROP COLUMN next_of_kin_name;
DROP TABLE member_register_batches;
COMMIT;
-- =============================================================================
-- ARGON — 0031: THE REGISTER. A SOCIETY'S MEMBERS, REGISTERED BY ITS SECRETARY.
--
-- Written against test/tier1/tier1_0031_test.go (2026-11-10), mutation-tested
-- with test/tier1/mutants_0031 before this file existed. The reviewer's
-- rulings of 2026-11-10 (§12): members arrive by import from the society's own
-- register; the secretary adds whoever joins next month; a member is ACTIVE,
-- the register being the society's own record; what is recorded is name,
-- member number, phone, branch, join date, collection channel, next of kin.
--
-- register_member is the ONE way the app adds a member (grants.sql: the app
-- has no INSERT on members, P70). It rests on the society's current
-- secretary's own step-up (act member_register), written in its transaction.
-- It checks the record: a name; a phone in +234 E.164 (sign-in is by phone;
-- the API turns 0803… and 234803… into it); one of the society's branches; a
-- join date not in the future; next of kin whole or absent (an old ledger
-- book often has none). The member number is the society's once (0001). It
-- writes the member's own person, ACTIVE, attributed to the act. No PIN and
-- no device: a member activates with an enrolment slip.
-- =============================================================================

BEGIN;

ALTER TABLE members ADD COLUMN next_of_kin_name TEXT;
ALTER TABLE members ADD COLUMN next_of_kin_phone TEXT;
ALTER TABLE members ADD COLUMN next_of_kin_relationship TEXT;
ALTER TABLE members ADD COLUMN registered_by_act UUID REFERENCES credential_acts(id);
ALTER TABLE members ADD COLUMN register_batch_id UUID;
ALTER TABLE members ADD COLUMN register_line INT;  -- the file line it came from, so a repeat answers the first commit's map

CREATE TABLE member_register_batches (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id    UUID NOT NULL REFERENCES societies(id),
  reference     TEXT NOT NULL,
  act_id        UUID REFERENCES credential_acts(id),
  row_count     INT NOT NULL,
  registered_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX member_register_batch_once ON member_register_batches (society_id, reference);
ALTER TABLE members ADD CONSTRAINT members_register_batch_fkey FOREIGN KEY (register_batch_id) REFERENCES member_register_batches(id);

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register'));

CREATE FUNCTION register_member(p_society UUID, p_branch_code TEXT, p_number TEXT, p_name TEXT, p_phone TEXT, p_joined DATE,
    p_channel collection_channel, p_nok_name TEXT, p_nok_phone TEXT, p_nok_relationship TEXT, p_act UUID, p_batch UUID, p_line INT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a        RECORD;
  v_user   UUID;
  v_member UUID;
  v_branch UUID;
  v_nok    INT;
BEGIN
  -- WHO: the society's current secretary, on their own act, from this transaction.
  -- (No act at all is refused here too: its kind is no kind.)
  SELECT kind, actor_user_id, act_xid INTO a FROM credential_acts WHERE id = p_act;
  IF a.kind IS DISTINCT FROM 'member_register' THEN
    PERFORM auth_refuse('member_register_act_kind', format('a member is registered on a member_register act, not %s', a.kind));
  END IF;
  IF a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('member_register_act_same_transaction', 'the act and the registration are one event: the act is written in its transaction');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM appointments ap JOIN members m ON m.id = ap.member_id
                  WHERE ap.society_id = p_society AND ap.post = 'secretary' AND m.user_id = a.actor_user_id
                    AND ap.held_from <= current_date AND (ap.held_until IS NULL OR ap.held_until >= current_date)) THEN
    PERFORM auth_refuse('member_registered_by_secretary', 'the society''s current secretary registers its members');
  END IF;
  -- WHAT.
  IF p_name IS NULL OR btrim(p_name) = '' THEN
    PERFORM auth_refuse('member_name_given', 'a member is registered by name');
  END IF;
  IF p_phone IS NULL OR p_phone !~ '^\+234[0-9]{10}$' THEN
    PERFORM auth_refuse('member_phone_e164', 'a member''s phone is a Nigerian number in +234 form (sign-in is by phone)');
  END IF;
  SELECT id INTO v_branch FROM branches WHERE society_id = p_society ORDER BY code = p_branch_code DESC, code LIMIT 1;
  IF v_branch IS NULL THEN
    PERFORM auth_refuse('member_branch_known', format('this society has no branch %s', coalesce(p_branch_code, '(none)')));
  END IF;
  IF p_joined IS NULL OR p_joined > current_date THEN
    PERFORM auth_refuse('member_joined_not_future', 'a member joined on a day that has come');
  END IF;
  v_nok := (CASE WHEN btrim(coalesce(p_nok_name, '')) <> '' THEN 1 ELSE 0 END)
         + (CASE WHEN btrim(coalesce(p_nok_phone, '')) <> '' THEN 1 ELSE 0 END)
         + (CASE WHEN btrim(coalesce(p_nok_relationship, '')) <> '' THEN 1 ELSE 0 END);
  IF v_nok NOT IN (0, 3) OR (v_nok = 3 AND p_nok_phone !~ '^\+234[0-9]{10}$') THEN
    PERFORM auth_refuse('member_next_of_kin_whole', 'a next of kin is whole (name, phone in +234 form, relationship) or absent');
  END IF;
  -- HOW: their own person, active, attributed.
  INSERT INTO users (full_name) VALUES (btrim(p_name)) RETURNING id INTO v_user;
  INSERT INTO members (society_id, branch_id, member_number, full_name, phone, status, collection_channel, joined_on, user_id,
                       next_of_kin_name, next_of_kin_phone, next_of_kin_relationship, registered_by_act, register_batch_id, register_line)
    VALUES (p_society, v_branch, btrim(p_number), btrim(p_name), p_phone, 'active', p_channel, p_joined, v_user,
            nullif(btrim(coalesce(p_nok_name, '')), ''), nullif(btrim(coalesce(p_nok_phone, '')), ''), nullif(btrim(coalesce(p_nok_relationship, '')), ''),
            p_act, p_batch, p_line)
    RETURNING id INTO v_member;
  RETURN v_member;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION register_member(UUID, TEXT, TEXT, TEXT, TEXT, DATE, collection_channel, TEXT, TEXT, TEXT, UUID, UUID, INT) FROM PUBLIC;

COMMIT;
