-- DELIBERATELY WRONG (m_mirror_unchecked). GENERATED (gen0042.py): a full copy of 0042 with
-- one rule broken, applied over 0042_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
DROP FUNCTION IF EXISTS dispute_state(UUID);
DROP VIEW IF EXISTS dispute_states;
DROP TRIGGER IF EXISTS dispute_correction_decisions_admissible ON dispute_correction_decisions;
DROP TABLE IF EXISTS dispute_correction_decisions;
DROP FUNCTION IF EXISTS dispute_decision_admissible();
DROP FUNCTION IF EXISTS dispute_post_reversal(UUID, UUID, UUID);
DROP TABLE IF EXISTS dispute_corrections;
DROP FUNCTION IF EXISTS dispute_correction_admissible();
DROP FUNCTION IF EXISTS dispute_correctable(UUID);
DROP TABLE IF EXISTS dispute_replies;
DROP FUNCTION IF EXISTS dispute_reply_admissible();
DROP TABLE IF EXISTS dispute_answers;
DROP FUNCTION IF EXISTS dispute_answer_admissible();
DROP TABLE IF EXISTS entry_disputes;
DROP FUNCTION IF EXISTS entry_dispute_admissible();
DROP FUNCTION IF EXISTS dispute_officer(UUID, UUID);
DROP TRIGGER IF EXISTS ledger_entries_reversal_mirrors ON ledger_entries;
DROP FUNCTION IF EXISTS reversal_mirrors();
DROP INDEX IF EXISTS ledger_entry_reversed_once;
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation'));
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation'));
COMMIT;

BEGIN;

ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal', 'approval_signature', 'approval_rejection',
  'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request', 'enrolment_issue',
  'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account', 'bank_name_confirmation',
  'dispute_correction', 'dispute_correction_decision'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release', 'signatory_reset_request',
  'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal', 'approval_signature',
  'approval_rejection', 'contribution_record', 'withdrawal_payout', 'withdrawal_authorisation', 'withdrawal_request',
  'enrolment_issue', 'member_register', 'payment_resolution', 'member_email', 'member_status', 'member_bank_account',
  'bank_name_confirmation', 'dispute_correction', 'dispute_correction_decision'));

-- ---- reversals, every one ---------------------------------------------------
CREATE UNIQUE INDEX ledger_entry_reversed_once ON ledger_entries (reverses_id) WHERE reverses_id IS NOT NULL;

CREATE FUNCTION reversal_mirrors() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.reverses_id IS NULL THEN
    RETURN NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM ledger_entries WHERE id = NEW.reverses_id AND reverses_id IS NOT NULL) THEN
    PERFORM auth_refuse('reversal_of_a_reversal', 'a reversal is not reversed: the original is posted again as a new entry');
  END IF;
  -- At commit, when the lines are written: the original's lines, negated, and nothing else.
  IF false AND EXISTS (
       (SELECT account_id, member_id, charge_role, -amount_kobo FROM ledger_lines WHERE entry_id = NEW.reverses_id
        EXCEPT ALL
        SELECT account_id, member_id, charge_role, amount_kobo FROM ledger_lines WHERE entry_id = NEW.id)
       UNION ALL
       (SELECT account_id, member_id, charge_role, amount_kobo FROM ledger_lines WHERE entry_id = NEW.id
        EXCEPT ALL
        SELECT account_id, member_id, charge_role, -amount_kobo FROM ledger_lines WHERE entry_id = NEW.reverses_id)) THEN
    PERFORM auth_refuse('reversal_mirrors_original', 'a reversal carries exactly the original''s lines, each negated (account, member, charge, amount): nothing more, nothing less');
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER ledger_entries_reversal_mirrors AFTER INSERT ON ledger_entries
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reversal_mirrors();

-- ---- the query -----------------------------------------------------------------
CREATE TABLE entry_disputes (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id UUID NOT NULL REFERENCES societies(id),
  entry_id   UUID NOT NULL REFERENCES ledger_entries(id),
  member_id  UUID NOT NULL REFERENCES members(id),
  raised_by  UUID NOT NULL REFERENCES users(id),
  question   TEXT NOT NULL CONSTRAINT dispute_question_said CHECK (length(btrim(question)) BETWEEN 10 AND 500),
  raised_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX entry_disputes_society ON entry_disputes (society_id, raised_at DESC);

CREATE TABLE dispute_answers (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  dispute_id  UUID NOT NULL REFERENCES entry_disputes(id),
  answered_by UUID NOT NULL REFERENCES users(id),
  answer      TEXT NOT NULL CONSTRAINT dispute_answer_said CHECK (length(btrim(answer)) BETWEEN 10 AND 1000),
  at          TIMESTAMPTZ NOT NULL
);

CREATE TABLE dispute_replies (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  dispute_id UUID NOT NULL REFERENCES entry_disputes(id),
  replied_by UUID NOT NULL REFERENCES users(id),
  reply      TEXT NOT NULL CONSTRAINT dispute_reply_known CHECK (reply IN ('accepted', 'reopened')),
  note       TEXT,
  at         TIMESTAMPTZ NOT NULL,
  CONSTRAINT dispute_reopened_says_why CHECK (reply <> 'reopened' OR length(btrim(coalesce(note, ''))) BETWEEN 10 AND 500)
);

CREATE TABLE dispute_corrections (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  dispute_id   UUID NOT NULL REFERENCES entry_disputes(id),
  proposed_by  UUID NOT NULL REFERENCES users(id),
  proposed_act UUID NOT NULL REFERENCES credential_acts(id),
  reason       TEXT NOT NULL CONSTRAINT dispute_correction_says_why CHECK (length(btrim(reason)) BETWEEN 10 AND 500),
  at           TIMESTAMPTZ NOT NULL
);

CREATE TABLE dispute_correction_decisions (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  correction_id UUID NOT NULL REFERENCES dispute_corrections(id),
  confirmed     BOOLEAN NOT NULL,
  decided_by    UUID NOT NULL REFERENCES users(id),
  decided_act   UUID NOT NULL REFERENCES credential_acts(id),
  reason        TEXT,
  entry_id      UUID REFERENCES ledger_entries(id),   -- the reversal a confirmation posted
  at            TIMESTAMPTZ NOT NULL,
  CONSTRAINT dispute_rejection_says_why CHECK (confirmed OR length(btrim(coalesce(reason, ''))) BETWEEN 10 AND 500)
);
CREATE UNIQUE INDEX dispute_correction_decided_once ON dispute_correction_decisions (correction_id);

-- Where a query stands, from its records, the latest first.
CREATE VIEW dispute_states AS
  SELECT d.id AS dispute_id, d.society_id, d.member_id, d.entry_id,
    CASE
      WHEN EXISTS (SELECT 1 FROM dispute_corrections c JOIN dispute_correction_decisions k ON k.correction_id = c.id
                    WHERE c.dispute_id = d.id AND k.confirmed) THEN 'corrected'
      WHEN EXISTS (SELECT 1 FROM dispute_corrections c WHERE c.dispute_id = d.id
                    AND NOT EXISTS (SELECT 1 FROM dispute_correction_decisions k WHERE k.correction_id = c.id)) THEN 'correction_proposed'
      ELSE coalesce((SELECT s FROM (
              SELECT a.at, a.id, 'answered' AS s FROM dispute_answers a WHERE a.dispute_id = d.id
              UNION ALL
              SELECT r.at, r.id, CASE r.reply WHEN 'accepted' THEN 'accepted' ELSE 'reopened' END FROM dispute_replies r WHERE r.dispute_id = d.id
              UNION ALL
              SELECT k.at, k.id, 'reopened' FROM dispute_corrections c JOIN dispute_correction_decisions k ON k.correction_id = c.id
               WHERE c.dispute_id = d.id AND NOT k.confirmed) e
            ORDER BY e.at DESC, e.id DESC LIMIT 1), 'open')
    END AS state
  FROM entry_disputes d;

-- A current officer of the society who is not the internal auditor.
CREATE FUNCTION dispute_officer(p_user UUID, p_society UUID) RETURNS BOOLEAN STABLE SECURITY DEFINER SET search_path = public, pg_temp LANGUAGE sql AS $$
  SELECT EXISTS (SELECT 1 FROM appointments ap JOIN members om ON om.id = ap.member_id
                  WHERE om.user_id = p_user AND ap.society_id = p_society AND ap.post <> 'internal_auditor'
                    AND ap.held_from <= auth_today() AND (ap.held_until IS NULL OR ap.held_until >= auth_today()))
$$;

CREATE FUNCTION dispute_state(p_dispute UUID) RETURNS TEXT STABLE SECURITY DEFINER SET search_path = public, pg_temp LANGUAGE sql AS $$
  SELECT state FROM dispute_states WHERE dispute_id = p_dispute
$$;

CREATE FUNCTION entry_dispute_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  m members%ROWTYPE;
BEGIN
  SELECT * INTO m FROM members WHERE id = NEW.member_id;
  NEW.society_id := m.society_id;
  NEW.raised_at := clock_timestamp();
  IF m.user_id IS DISTINCT FROM NEW.raised_by THEN
    PERFORM auth_refuse('dispute_by_member', 'an entry is queried by the member it names, themselves');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM ledger_entries e JOIN ledger_lines l ON l.entry_id = e.id
                  WHERE e.id = NEW.entry_id AND e.society_id = m.society_id AND l.member_id = m.id) THEN
    PERFORM auth_refuse('dispute_on_own_entry', 'a member queries an entry of their own society that names them');
  END IF;
  IF EXISTS (SELECT 1 FROM dispute_states s WHERE s.entry_id = NEW.entry_id AND s.member_id = NEW.member_id
              AND s.state NOT IN ('accepted', 'corrected')) THEN
    PERFORM auth_refuse('dispute_once_open', 'this entry is already queried and the query is not closed: reply to it instead');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER entry_disputes_admissible BEFORE INSERT ON entry_disputes FOR EACH ROW EXECUTE FUNCTION entry_dispute_admissible();

CREATE FUNCTION dispute_answer_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  d entry_disputes%ROWTYPE;
BEGIN
  SELECT * INTO d FROM entry_disputes WHERE id = NEW.dispute_id FOR UPDATE;
  NEW.at := clock_timestamp();
  IF NOT dispute_officer(NEW.answered_by, d.society_id) THEN
    PERFORM auth_refuse('dispute_answered_by_officer', 'a query is answered by a current officer of the society, not the internal auditor');
  END IF;
  IF NEW.answered_by = d.raised_by THEN
    PERFORM auth_refuse('dispute_not_own', 'nobody answers a query about their own entry');
  END IF;
  IF dispute_state(d.id) NOT IN ('open', 'reopened') THEN
    PERFORM auth_refuse('dispute_awaits_answer', 'this query is not waiting for an answer: it is answered, being corrected, or closed');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER dispute_answers_admissible BEFORE INSERT ON dispute_answers FOR EACH ROW EXECUTE FUNCTION dispute_answer_admissible();

CREATE FUNCTION dispute_reply_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  d entry_disputes%ROWTYPE;
BEGIN
  SELECT * INTO d FROM entry_disputes WHERE id = NEW.dispute_id FOR UPDATE;
  NEW.at := clock_timestamp();
  IF NEW.replied_by IS DISTINCT FROM d.raised_by THEN
    PERFORM auth_refuse('dispute_reply_by_member', 'an answer is accepted or reopened by the member who asked');
  END IF;
  IF dispute_state(d.id) <> 'answered' THEN
    PERFORM auth_refuse('dispute_awaits_reply', 'there is no answer waiting for the member''s reply');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER dispute_replies_admissible BEFORE INSERT ON dispute_replies FOR EACH ROW EXECUTE FUNCTION dispute_reply_admissible();

-- What a query's correction may reverse: an entry an officer recorded by hand.
CREATE FUNCTION dispute_correctable(p_entry UUID) RETURNS BOOLEAN STABLE SECURITY DEFINER SET search_path = public, pg_temp LANGUAGE sql AS $$
  SELECT EXISTS (SELECT 1 FROM ledger_entries e WHERE e.id = p_entry
                  AND e.kind IN ('contribution_in', 'withdrawal_out', 'opening_balance')
                  AND e.vfd_inflow_id IS NULL AND e.vfd_outcome_id IS NULL
                  AND e.reverses_id IS NULL AND e.adjusts_id IS NULL
                  AND NOT EXISTS (SELECT 1 FROM ledger_entries r WHERE r.reverses_id = e.id))
$$;

CREATE FUNCTION dispute_correction_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  d entry_disputes%ROWTYPE;
  a credential_acts%ROWTYPE;
BEGIN
  SELECT * INTO d FROM entry_disputes WHERE id = NEW.dispute_id FOR UPDATE;
  NEW.at := clock_timestamp();
  SELECT * INTO a FROM credential_acts WHERE id = NEW.proposed_act;
  IF NOT FOUND OR a.kind <> 'dispute_correction' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('dispute_correction_act', 'a correction is proposed on the proposer''s own dispute_correction act, taken with it');
  END IF;
  NEW.proposed_by := a.actor_user_id;
  IF NOT dispute_officer(NEW.proposed_by, d.society_id) THEN
    PERFORM auth_refuse('dispute_correction_by_officer', 'a correction is proposed by a current officer of the society, not the internal auditor');
  END IF;
  IF NEW.proposed_by = d.raised_by THEN
    PERFORM auth_refuse('dispute_not_own', 'nobody corrects an entry they queried themselves');
  END IF;
  IF dispute_state(d.id) IN ('accepted', 'corrected', 'correction_proposed') THEN
    PERFORM auth_refuse('dispute_correction_open', 'this query is closed, or a correction is already waiting for a second officer');
  END IF;
  IF NOT dispute_correctable(d.entry_id) THEN
    PERFORM auth_refuse('dispute_correctable', 'this entry is not corrected by officers: it was posted on VFD''s word (the money moved at the bank), is itself a correction, or is already reversed');
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER dispute_corrections_admissible BEFORE INSERT ON dispute_corrections FOR EACH ROW EXECUTE FUNCTION dispute_correction_admissible();

-- The reversal a confirmed correction posts: the disputed entry's lines,
-- negated, today. Its actor is the confirming officer, on their decision's
-- act (an entry names who did it unless VFD did: entry_names_actor_or_vfd);
-- the proposer is named in its label.
CREATE FUNCTION dispute_post_reversal(p_correction UUID, p_act UUID, p_by UUID) RETURNS UUID SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c dispute_corrections%ROWTYPE;
  d entry_disputes%ROWTYPE;
  o ledger_entries%ROWTYPE;
  e UUID;
  post officer_post;
BEGIN
  SELECT * INTO c FROM dispute_corrections WHERE id = p_correction;
  SELECT * INTO d FROM entry_disputes WHERE id = c.dispute_id;
  SELECT * INTO o FROM ledger_entries WHERE id = d.entry_id;
  SELECT ap.post INTO post FROM appointments ap JOIN members om ON om.id = ap.member_id
   WHERE om.user_id = p_by AND ap.society_id = o.society_id
     AND ap.held_from <= auth_today() AND (ap.held_until IS NULL OR ap.held_until >= auth_today())
   ORDER BY ap.is_signatory DESC, ap.held_from DESC LIMIT 1;   -- a description: who may confirm is the decision's rule
  INSERT INTO ledger_entries (society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, reverses_id, reason, client_reference, act_id)
    VALUES (o.society_id, o.kind, society_today(), p_by, post,
            coalesce(initcap(replace(post::text, '_', ' ')) || ', confirming', 'Confirming') || ' a correction proposed by ' || coalesce((SELECT full_name FROM users WHERE id = c.proposed_by), 'another officer'),
            o.id, 'On the member''s query: ' || c.reason, 'dispute-correction:' || c.id, p_act)
    RETURNING id INTO e;
  INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id, charge_role)
    SELECT e, l.account_id, -l.amount_kobo, l.member_id, l.charge_role FROM ledger_lines l WHERE l.entry_id = o.id;
  RETURN e;
END $$ LANGUAGE plpgsql;
REVOKE ALL ON FUNCTION dispute_post_reversal(UUID, UUID, UUID) FROM PUBLIC;

CREATE FUNCTION dispute_decision_admissible() RETURNS trigger SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c dispute_corrections%ROWTYPE;
  d entry_disputes%ROWTYPE;
  a credential_acts%ROWTYPE;
BEGIN
  SELECT * INTO c FROM dispute_corrections WHERE id = NEW.correction_id;
  SELECT * INTO d FROM entry_disputes WHERE id = c.dispute_id FOR UPDATE;
  NEW.at := clock_timestamp();
  NEW.entry_id := NULL;
  SELECT * INTO a FROM credential_acts WHERE id = NEW.decided_act;
  IF NOT FOUND OR a.kind <> 'dispute_correction_decision' OR a.act_xid IS DISTINCT FROM pg_current_xact_id() THEN
    PERFORM auth_refuse('dispute_decision_act', 'a correction is decided on the decider''s own dispute_correction_decision act, taken with it');
  END IF;
  NEW.decided_by := a.actor_user_id;
  IF NOT dispute_officer(NEW.decided_by, d.society_id) THEN
    PERFORM auth_refuse('dispute_decision_by_officer', 'a correction is decided by a current officer of the society, not the internal auditor');
  END IF;
  IF NEW.decided_by = c.proposed_by THEN
    PERFORM auth_refuse('dispute_second_officer', 'a correction needs a second officer: whoever proposed it does not decide it (D-13)');
  END IF;
  IF NEW.decided_by = d.raised_by THEN
    PERFORM auth_refuse('dispute_not_own', 'nobody decides a correction of an entry they queried themselves');
  END IF;
  IF NEW.confirmed THEN
    IF NOT dispute_correctable(d.entry_id) THEN
      PERFORM auth_refuse('dispute_correctable', 'this entry can no longer be corrected this way: it is already reversed');
    END IF;
    NEW.entry_id := dispute_post_reversal(c.id, NEW.decided_act, NEW.decided_by);
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER dispute_correction_decisions_admissible BEFORE INSERT ON dispute_correction_decisions FOR EACH ROW EXECUTE FUNCTION dispute_decision_admissible();

CREATE TRIGGER entry_disputes_append_only BEFORE UPDATE OR DELETE ON entry_disputes FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER dispute_answers_append_only BEFORE UPDATE OR DELETE ON dispute_answers FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER dispute_replies_append_only BEFORE UPDATE OR DELETE ON dispute_replies FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER dispute_corrections_append_only BEFORE UPDATE OR DELETE ON dispute_corrections FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();
CREATE TRIGGER dispute_correction_decisions_append_only BEFORE UPDATE OR DELETE ON dispute_correction_decisions FOR EACH ROW EXECUTE FUNCTION vfd_records_append_only();

COMMIT;
