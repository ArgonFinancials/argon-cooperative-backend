-- DELIBERATELY WRONG (m_checked_twice). GENERATED (gen0040.py): a full copy of 0040 with
-- one rule broken, applied over 0040_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
CREATE OR REPLACE FUNCTION vfd_transfer_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  h withdrawal_holds%ROWTYPE;
BEGIN
  SELECT * INTO h FROM withdrawal_holds WHERE id = NEW.hold_id FOR UPDATE;   -- one transfer decision at a time per hold
  IF NEW.reference IS DISTINCT FROM vfd_transfer_reference(h.id) THEN
    PERFORM auth_refuse('vfd_reference_is_the_holds', format('a transfer''s reference is derived from its hold: %s, not %s', vfd_transfer_reference(h.id), NEW.reference));
  END IF;
  IF NEW.amount_kobo IS DISTINCT FROM h.amount_kobo THEN
    PERFORM auth_refuse('vfd_transfer_is_the_holds', format('a transfer moves what was held: %s kobo, not %s', h.amount_kobo, NEW.amount_kobo));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM vfd_accounts WHERE member_id = h.member_id AND account_number = NEW.to_account) THEN
    PERFORM auth_refuse('vfd_transfer_to_members_account', 'a payout goes into the member''s own VFD account and no other (§12, 2026-11-10); this member has none, or this is not it');
  END IF;
  -- From the pool (0039, §1.1): every transfer leaves Argon's VFD pool, the
  -- account VFD's enquiry names; a society's account holds nothing (VFD sweeps
  -- its deposits into the pool).
  IF NOT EXISTS (SELECT 1 FROM vfd_pool WHERE account_number = NEW.from_account) THEN
    PERFORM auth_refuse('vfd_transfer_from_pool', 'a payout leaves from Argon''s VFD pool, the account VFD''s enquiry names (§1.1); none is recorded, or this is not it');
  END IF;
  IF EXISTS (SELECT 1 FROM withdrawal_hold_releases WHERE hold_id = h.id) OR EXISTS (SELECT 1 FROM ledger_entries WHERE hold_id = h.id) THEN
    PERFORM auth_refuse('vfd_transfer_on_open_hold', 'a transfer is sent on an open hold: this one was released or paid');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS vfd_transfers_to_bank ON vfd_transfers;
DROP FUNCTION IF EXISTS vfd_transfer_to_bank();
ALTER TABLE vfd_transfers DROP COLUMN IF EXISTS to_bank;
DROP TRIGGER IF EXISTS signatory_approvals_bank_setter ON signatory_approvals;
DROP FUNCTION IF EXISTS signature_not_bank_setter();
DROP TRIGGER IF EXISTS withdrawal_holds_bank ON withdrawal_holds;
DROP FUNCTION IF EXISTS withdrawal_hold_bank();
DROP TRIGGER IF EXISTS approval_requests_bank ON approval_requests;
DROP FUNCTION IF EXISTS withdrawal_request_bank();
DROP FUNCTION IF EXISTS society_on_vfd(UUID);
DROP VIEW IF EXISTS member_bank_current;
DROP TABLE IF EXISTS bank_name_confirmations;
DROP FUNCTION IF EXISTS bank_name_confirmation_admissible();
DROP TABLE IF EXISTS bank_name_checks;
DROP FUNCTION IF EXISTS bank_name_check_admissible();
DROP TABLE IF EXISTS member_bank_accounts;
DROP FUNCTION IF EXISTS member_bank_account_admissible();
DROP FUNCTION IF EXISTS names_resemble(TEXT, TEXT);
DROP FUNCTION IF EXISTS name_words(TEXT);
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status'));
COMMIT;

BEGIN;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation'));

-- names_resemble: two names share at least two words (of two letters or
-- more, any order, case and punctuation aside), or one, when either name is
-- a single word. A bank gives SURNAME FIRSTNAME MIDDLE; a register gives
-- them in any order, and often not the middle one.
CREATE FUNCTION name_words(n TEXT) RETURNS TEXT[] IMMUTABLE LANGUAGE sql AS $$
  SELECT coalesce(array_agg(DISTINCT w), '{}') FROM regexp_split_to_table(upper(regexp_replace(coalesce(n, ''), '[^A-Za-z0-9]+', ' ', 'g')), ' ') AS w
   WHERE length(w) >= 2
$$;
CREATE FUNCTION names_resemble(a TEXT, b TEXT) RETURNS BOOLEAN IMMUTABLE LANGUAGE sql AS $$
  SELECT CASE WHEN cardinality(name_words(a)) = 0 OR cardinality(name_words(b)) = 0 THEN false
              WHEN cardinality(name_words(a)) = 1 OR cardinality(name_words(b)) = 1
                THEN cardinality(ARRAY(SELECT unnest(name_words(a)) INTERSECT SELECT unnest(name_words(b)))) >= 1
              ELSE cardinality(ARRAY(SELECT unnest(name_words(a)) INTERSECT SELECT unnest(name_words(b)))) >= 2 END
$$;

CREATE TABLE member_bank_accounts (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  member_id      UUID NOT NULL REFERENCES members(id),
  society_id     UUID NOT NULL REFERENCES societies(id),
  account_number TEXT NOT NULL CONSTRAINT bank_account_number_shape CHECK (account_number ~ '^[0-9]{10}$'),
  bank_code      TEXT NOT NULL CONSTRAINT bank_code_shape CHECK (bank_code ~ '^[0-9]{3,6}$'),
  set_by_act     UUID NOT NULL REFERENCES credential_acts(id),
  set_by_user    UUID NOT NULL REFERENCES users(id),
  by_member      BOOLEAN NOT NULL,
  recorded_at    TIMESTAMPTZ NOT NULL
);
CREATE INDEX member_bank_accounts_member ON member_bank_accounts (member_id, recorded_at DESC);

CREATE FUNCTION member_bank_account_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a credential_acts%ROWTYPE;
  m members%ROWTYPE;
BEGIN
  SELECT * INTO m FROM members WHERE id = NEW.member_id FOR UPDATE;   -- one change at a time per member
  NEW.society_id := m.society_id;
  NEW.recorded_at := clock_timestamp();
  SELECT * INTO a FROM credential_acts WHERE id = NEW.set_by_act;
  IF NOT FOUND OR a.kind <> 'member_bank_account' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('bank_account_act', 'a member''s bank details are set on the setter''s own member_bank_account act, taken with them');
  END IF;
  NEW.set_by_user := a.actor_user_id;
  NEW.by_member := (m.user_id IS NOT DISTINCT FROM a.actor_user_id);
  IF NOT NEW.by_member THEN
    -- The officer path: a current officer of the society (not the auditor),
    -- for a member who cannot sign in to set their own (no PIN yet).
    IF NOT EXISTS (SELECT 1 FROM appointments ap JOIN members om ON om.id = ap.member_id
                    WHERE om.user_id = a.actor_user_id AND ap.society_id = m.society_id AND ap.post <> 'internal_auditor'
                      AND ap.held_from <= auth_today() AND (ap.held_until IS NULL OR ap.held_until >= auth_today())) THEN
      PERFORM auth_refuse('bank_account_by_member', 'a member''s bank details are set by the member, on their own phone; an officer of the society sets them only for a member who cannot sign in');
    END IF;
    IF m.user_id IS NOT NULL AND EXISTS (SELECT 1 FROM auth_credentials c WHERE c.user_id = m.user_id) THEN
      PERFORM auth_refuse('bank_account_member_can_sign_in', 'this member can sign in: they set their own bank details, on their own phone');
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER member_bank_accounts_admissible BEFORE INSERT ON member_bank_accounts FOR EACH ROW EXECUTE FUNCTION member_bank_account_admissible();
CREATE TRIGGER member_bank_accounts_append_only BEFORE UPDATE OR DELETE ON member_bank_accounts FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

-- What VFD said about a set of details: one answer each, as argon_wallet.
-- found: VFD named the account (vfd_name, its name); not found: VFD said
-- there is no such account at that bank (the details are no good: set
-- again). No answer is not recorded: the worker asks again.
CREATE TABLE bank_name_checks (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bank_account_id UUID NOT NULL REFERENCES member_bank_accounts(id),
  found           BOOLEAN NOT NULL,
  vfd_code        TEXT NOT NULL,
  vfd_name        TEXT,
  name_check      TEXT CONSTRAINT bank_name_check_known CHECK (name_check IN ('matches', 'differs')),
  checked_at      TIMESTAMPTZ NOT NULL,
  CONSTRAINT bank_name_when_found CHECK (found = (vfd_name IS NOT NULL AND btrim(vfd_name) <> '') AND found = (name_check IS NOT NULL))
);

CREATE FUNCTION bank_name_check_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  NEW.checked_at := clock_timestamp();
  -- Matches or differs: the database's comparison with the register, never
  -- the writer's word.
  NEW.name_check := CASE WHEN NOT NEW.found THEN NULL
                         WHEN names_resemble(NEW.vfd_name, (SELECT m.full_name FROM member_bank_accounts b JOIN members m ON m.id = b.member_id WHERE b.id = NEW.bank_account_id))
                           THEN 'matches' ELSE 'differs' END;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER bank_name_checks_admissible BEFORE INSERT ON bank_name_checks FOR EACH ROW EXECUTE FUNCTION bank_name_check_admissible();
CREATE TRIGGER bank_name_checks_append_only BEFORE UPDATE OR DELETE ON bank_name_checks FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

CREATE TABLE bank_name_confirmations (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  bank_account_id UUID NOT NULL REFERENCES member_bank_accounts(id),
  act_id          UUID NOT NULL REFERENCES credential_acts(id),
  confirmed_by    UUID NOT NULL REFERENCES users(id),
  -- What the signatory looked at, and why the name is the member's.
  reason          TEXT NOT NULL CONSTRAINT bank_name_confirmation_says_why CHECK (length(btrim(reason)) > 10),
  at              TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX bank_name_confirmed_once ON bank_name_confirmations (bank_account_id);

CREATE FUNCTION bank_name_confirmation_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a credential_acts%ROWTYPE;
  b member_bank_accounts%ROWTYPE;
BEGIN
  SELECT * INTO b FROM member_bank_accounts WHERE id = NEW.bank_account_id;
  SELECT * INTO a FROM credential_acts WHERE id = NEW.act_id;
  IF NOT FOUND OR a.kind <> 'bank_name_confirmation' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('bank_name_confirmation_act', 'a name is confirmed on the confirmer''s own bank_name_confirmation act, taken with it');
  END IF;
  NEW.confirmed_by := a.actor_user_id;
  NEW.at := clock_timestamp();
  IF NOT EXISTS (SELECT 1 FROM bank_name_checks k WHERE k.bank_account_id = b.id AND k.name_check = 'differs') THEN
    PERFORM auth_refuse('bank_name_confirmation_needed', 'only a name the bank gave that does not match the register is confirmed by a person');
  END IF;
  IF NOT person_holds_post(a.actor_user_id, b.society_id, true) THEN
    PERFORM auth_refuse('bank_name_confirmed_by_signatory', 'a name that does not match the register is confirmed by a current signatory of the society');
  END IF;
  IF a.actor_user_id IN (b.set_by_user, (SELECT user_id FROM members WHERE id = b.member_id)) THEN
    PERFORM auth_refuse('bank_name_confirmed_by_another', 'whoever set the bank details, and the member, never confirm their name: another signatory does');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER bank_name_confirmations_admissible BEFORE INSERT ON bank_name_confirmations FOR EACH ROW EXECUTE FUNCTION bank_name_confirmation_admissible();
CREATE TRIGGER bank_name_confirmations_append_only BEFORE UPDATE OR DELETE ON bank_name_confirmations FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

-- The member's current details: the latest set, with what VFD said.
CREATE VIEW member_bank_current AS
  SELECT DISTINCT ON (b.member_id) b.id, b.member_id, b.society_id, b.account_number, b.bank_code, b.set_by_act,
         b.set_by_user, b.by_member, b.recorded_at,
         (k.id IS NOT NULL) AS checked, coalesce(k.found, false) AS found, k.vfd_name, k.name_check, k.checked_at,
         c.confirmed_by, c.at AS confirmed_at,
         coalesce(k.found AND (k.name_check = 'matches' OR c.id IS NOT NULL), false) AS name_settled
    FROM member_bank_accounts b
    LEFT JOIN bank_name_checks k ON k.bank_account_id = b.id
    LEFT JOIN bank_name_confirmations c ON c.bank_account_id = b.id
   ORDER BY b.member_id, b.recorded_at DESC, b.id DESC;

-- A society on VFD: its own VFD account is recorded (there the system pays).
CREATE FUNCTION society_on_vfd(s UUID) RETURNS BOOLEAN STABLE SECURITY DEFINER SET search_path = public, pg_temp LANGUAGE sql AS $$
  SELECT EXISTS (SELECT 1 FROM vfd_accounts WHERE society_id = s AND member_id IS NULL)
$$;

-- When the withdrawal is asked for: no bank details, no request. The member
-- reads this.
CREATE FUNCTION withdrawal_request_bank() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.kind = 'withdrawal_out' AND society_on_vfd(NEW.society_id)
     AND NOT EXISTS (SELECT 1 FROM member_bank_accounts WHERE member_id = NEW.member_id) THEN
    PERFORM auth_refuse('payout_needs_bank_account', 'Add your bank account (its number and your bank) before you ask for a withdrawal: it is where the money goes.');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_requests_bank BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION withdrawal_request_bank();

-- At the hold (the last signature), for a society on VFD: the member's
-- current bank details exist, VFD named them, and their name is settled.
CREATE FUNCTION withdrawal_hold_bank() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  b RECORD;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM credential_acts WHERE id = NEW.act_id AND kind = 'approval_signature') THEN
    RETURN NEW;   -- an officer's authorisation (0012): the officers pay it by hand
  END IF;
  IF NOT society_on_vfd(NEW.society_id) THEN
    RETURN NEW;   -- a society not on VFD: no system payout
  END IF;
  SELECT * INTO b FROM member_bank_current WHERE member_id = NEW.member_id;
  IF NOT FOUND THEN
    PERFORM auth_refuse('payout_needs_bank_account', 'Add your bank account (its number and your bank) before a withdrawal can be paid: it is where the money goes.');
  END IF;
  IF NOT b.checked THEN
    PERFORM auth_refuse('payout_bank_not_checked', 'Your bank account is still being checked with your bank. A withdrawal can be paid into it once that is done.');
  END IF;
  IF NOT b.found THEN
    PERFORM auth_refuse('payout_bank_not_found', 'Your bank says there is no such account. Add your bank account again (its number and your bank) before a withdrawal can be paid.');
  END IF;
  IF NOT b.name_settled THEN
    PERFORM auth_refuse('payout_bank_name_unconfirmed', 'The name your bank gives for your account does not match your name on the register. An officer must confirm it before a withdrawal can be paid into it.');
  END IF;
  -- Whoever set the details never signs (signature_not_bank_setter, below):
  -- the last signature is checked as it is written, with this hold.
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER withdrawal_holds_bank BEFORE INSERT ON withdrawal_holds FOR EACH ROW EXECUTE FUNCTION withdrawal_hold_bank();

-- CHANGING THEM IS A SECURITY ACT: whoever set a member's details (other
-- than the member) signs none of that member's withdrawals, the last included.
CREATE FUNCTION signature_not_bank_setter() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN member_bank_current b ON b.member_id = r.member_id
              WHERE r.id = NEW.request_id AND r.kind = 'withdrawal_out' AND NOT b.by_member
                AND b.set_by_user = (SELECT user_id FROM members WHERE id = NEW.member_id)) THEN
    PERFORM auth_refuse('bank_setter_authorises', 'you set this member''s bank details: you never authorise a payout to them. Another signatory signs');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER signatory_approvals_bank_setter BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION signature_not_bank_setter();

-- A transfer goes to the member's current bank account, at its bank.
ALTER TABLE vfd_transfers ADD COLUMN to_bank TEXT;
CREATE FUNCTION vfd_transfer_to_bank() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM member_bank_current b JOIN withdrawal_holds h ON h.member_id = b.member_id
                  WHERE h.id = NEW.hold_id AND b.account_number = NEW.to_account AND b.bank_code = NEW.to_bank AND b.name_settled) THEN
    PERFORM auth_refuse('vfd_transfer_to_members_bank', 'a payout goes into the member''s own bank account, as their current bank details name it, its name settled (0040)');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER vfd_transfers_to_bank BEFORE INSERT ON vfd_transfers FOR EACH ROW EXECUTE FUNCTION vfd_transfer_to_bank();

-- No longer into the member's VFD account (swept into the pool, §1.1).
CREATE OR REPLACE FUNCTION vfd_transfer_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  h withdrawal_holds%ROWTYPE;
BEGIN
  SELECT * INTO h FROM withdrawal_holds WHERE id = NEW.hold_id FOR UPDATE;   -- one transfer decision at a time per hold
  IF NEW.reference IS DISTINCT FROM vfd_transfer_reference(h.id) THEN
    PERFORM auth_refuse('vfd_reference_is_the_holds', format('a transfer''s reference is derived from its hold: %s, not %s', vfd_transfer_reference(h.id), NEW.reference));
  END IF;
  IF NEW.amount_kobo IS DISTINCT FROM h.amount_kobo THEN
    PERFORM auth_refuse('vfd_transfer_is_the_holds', format('a transfer moves what was held: %s kobo, not %s', h.amount_kobo, NEW.amount_kobo));
  END IF;
  -- Where it goes: the member's own bank account (0040's vfd_transfer_to_bank).
  -- From the pool (0039, §1.1): every transfer leaves Argon's VFD pool, the
  -- account VFD's enquiry names; a society's account holds nothing (VFD sweeps
  -- its deposits into the pool).
  IF NOT EXISTS (SELECT 1 FROM vfd_pool WHERE account_number = NEW.from_account) THEN
    PERFORM auth_refuse('vfd_transfer_from_pool', 'a payout leaves from Argon''s VFD pool, the account VFD''s enquiry names (§1.1); none is recorded, or this is not it');
  END IF;
  IF EXISTS (SELECT 1 FROM withdrawal_hold_releases WHERE hold_id = h.id) OR EXISTS (SELECT 1 FROM ledger_entries WHERE hold_id = h.id) THEN
    PERFORM auth_refuse('vfd_transfer_on_open_hold', 'a transfer is sent on an open hold: this one was released or paid');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

COMMIT;
