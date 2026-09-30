-- =============================================================================
-- ARGON — 0002: USERS, SIGNATORY QUORUM, LEDGER EDGES, PERIODS, ADJUSTMENTS
--
-- Written against test/tier1/tier1_0002_test.go, which was reviewed first and
-- mutation-tested (test/tier1/mutate.sh). Every rule here names its constraint
-- as the tests expect; a trigger reports it with
--   RAISE ... USING ERRCODE = 'check_violation', CONSTRAINT = '<name>'.
--
-- Read /LEDGER_CONTRACT.md before changing anything here.
--
-- REFUSES TO RUN, rather than silently losing or inventing data, if the
-- database already holds rows this migration cannot carry across honestly.
-- =============================================================================

BEGIN;

CREATE EXTENSION IF NOT EXISTS btree_gist;   -- period overlap exclusion

-- -----------------------------------------------------------------------------
-- Preconditions. Each names what would otherwise be lost or made up.
-- -----------------------------------------------------------------------------
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM ledger_entries WHERE member_id IS NOT NULL) THEN
    RAISE EXCEPTION '0002: ledger_entries.member_id is populated. Moving it onto lines means deciding which '
                    'line of each entry is the member''s — a human decision, not a migration''s.';
  END IF;
  IF EXISTS (SELECT 1 FROM ledger_entries e JOIN ledger_batches b ON b.id = e.batch_id
             WHERE e.kind = 'opening_balance' AND b.as_of IS NULL) THEN
    RAISE EXCEPTION '0002: opening balances exist whose as-at date was unknown. 0001 dated them with the '
                    'posting date (the §7b defect). They are immutable; correct them by reversal before 0002.';
  END IF;
  IF EXISTS (SELECT 1 FROM ledger_entries e JOIN ledger_batches b ON b.id = e.batch_id
             WHERE b.as_of IS DISTINCT FROM e.value_date AND e.kind = 'opening_balance') THEN
    RAISE EXCEPTION '0002: ledger_batches.as_of disagrees with an entry''s value_date. Dropping as_of would '
                    'discard one of two conflicting records; resolve which is true first.';
  END IF;
  IF EXISTS (SELECT 1 FROM signatory_approvals) THEN
    RAISE EXCEPTION '0002: signatures exist. Their signer (user) cannot be derived honestly — '
                    'members.user_id is new in this migration and is NULL for every record.';
  END IF;
END $$;

-- =============================================================================
-- USERS — the person. A member record belongs to one; one person may be two
-- member records (the import's duplicate case).
-- =============================================================================

CREATE TABLE users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  full_name  TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE members ADD COLUMN user_id UUID REFERENCES users(id);
CREATE INDEX members_user_idx ON members (user_id) WHERE user_id IS NOT NULL;

-- Every ledger actor is a user. Closes the 0001 gap: actor_user_id pointed at
-- nothing.
ALTER TABLE ledger_entries
  ADD CONSTRAINT ledger_entries_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES users(id);

-- Serialises every check that asks "what does this PERSON hold", so two
-- concurrent writes cannot each pass a check the other would have failed.
CREATE FUNCTION lock_person(person UUID, fallback UUID) RETURNS void AS $$
  SELECT pg_advisory_xact_lock(hashtextextended('argon.person:' || coalesce(person, fallback)::text, 0));
$$ LANGUAGE sql;

-- The society's calendar date. Appointment terms are dates in the society's
-- own records, which are kept in Nigerian time, not the server's.
CREATE FUNCTION society_today() RETURNS date AS $$
  SELECT (now() AT TIME ZONE 'Africa/Lagos')::date;
$$ LANGUAGE sql STABLE;

-- =============================================================================
-- G1 — A POSTED ENTRY DOES NOT GROW (§2)
--
-- posted_at is a fact about when the SERVER wrote the row; a caller has no
-- standing to assert it. Server-generated only — a supplied value is refused,
-- not checked. (And 0001's trigger already refuses any later UPDATE of it.)
--
-- "Posted" means "committed by a transaction other than this one". That is
-- NOT "posted_at < now()": now() is the START of the current transaction, so a
-- transaction that began before an entry was committed has an earlier now()
-- and would read that entry as still open (test: OlderTransaction...). The
-- entry therefore also records which transaction wrote it. Lines are accepted
-- only while BOTH match the current transaction:
--   posting_xid = pg_current_xact_id()  distinguishes concurrent transactions
--                                        that start in the same microsecond;
--   posted_at   = now()                 distinguishes a restored database, whose
--                                        xid counter restarts and could repeat an
--                                        old entry's posting_xid.
-- Both are server-generated and neither can be supplied.
-- =============================================================================

ALTER TABLE ledger_entries ADD COLUMN posting_xid xid8 NOT NULL DEFAULT pg_current_xact_id();

CREATE FUNCTION ledger_entry_server_facts() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.posted_at IS DISTINCT FROM now() OR NEW.posting_xid IS DISTINCT FROM pg_current_xact_id() THEN
    RAISE EXCEPTION 'posted_at is written by the server, not supplied: got %, the posting time is %',
      NEW.posted_at, now()
      USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_entries_posted_at_is_now';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_entries_a_server_facts
  BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION ledger_entry_server_facts();

-- =============================================================================
-- M — A MEMBER'S BALANCE IS A FACT ABOUT LINES
--
-- ledger_entries.member_id is DROPPED, not constrained to agree with the
-- lines: a constraint keeps two records in step, dropping one leaves one.
-- =============================================================================

ALTER TABLE ledger_lines
  ADD COLUMN member_id UUID CONSTRAINT ledger_lines_member_id_fkey REFERENCES members(id);
CREATE INDEX ledger_lines_member_idx ON ledger_lines (member_id) WHERE member_id IS NOT NULL;

DROP INDEX ledger_entries_member_idx;
ALTER TABLE ledger_entries DROP COLUMN member_id;

CREATE FUNCTION ledger_line_admissible() RETURNS TRIGGER AS $$
DECLARE
  e ledger_entries%ROWTYPE;
BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF NOT FOUND THEN
    RETURN NEW;   -- the foreign key refuses it, by name
  END IF;

  IF NOT (e.posting_xid = pg_current_xact_id() AND e.posted_at = now()) THEN
    RAISE EXCEPTION 'entry % is posted: lines cannot be added to it. A correction is a reversal and a '
                    'repost, or an adjusting entry (LEDGER_CONTRACT.md §2).', NEW.entry_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_lines_entry_open';
  END IF;

  -- A member that does not exist is the foreign key's to refuse, by its name;
  -- only an existing member of ANOTHER society is refused here.
  IF NEW.member_id IS NOT NULL
     AND (SELECT society_id FROM members WHERE id = NEW.member_id) <> e.society_id THEN
    RAISE EXCEPTION 'line posts to member % of another society', NEW.member_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'ledger_line_member_in_society';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_lines_admissible
  BEFORE INSERT ON ledger_lines FOR EACH ROW EXECUTE FUNCTION ledger_line_admissible();

-- =============================================================================
-- V — AN UNKNOWN AS-AT DATE IS NULL, NEVER TODAY (§7b)
--
-- NULL is a stated fact ("accumulated before migration"), not a missing field.
-- Only an opening balance may carry it: every other movement happens on a day
-- the system saw. ledger_batches.as_of was a second record of the same date
-- and is dropped; the batch's as-at date is read from its entries.
-- =============================================================================

ALTER TABLE ledger_entries ALTER COLUMN value_date DROP NOT NULL;
ALTER TABLE ledger_entries ADD CONSTRAINT value_date_known_unless_opening_balance
  CHECK (value_date IS NOT NULL OR kind = 'opening_balance');
COMMENT ON COLUMN ledger_entries.value_date IS
  'The society''s own as-at date where one applies, else the posting date. NULL only for an '
  'opening_balance whose records carry no as-at date: show it as "accumulated before migration", never blank.';

ALTER TABLE ledger_batches DROP COLUMN as_of;

-- =============================================================================
-- A — AN ADJUSTMENT REFERENCES WHAT IT CORRECTS (§2, §5)
-- =============================================================================

ALTER TABLE ledger_entries
  ADD COLUMN adjusts_id UUID CONSTRAINT ledger_entries_adjusts_id_fkey REFERENCES ledger_entries(id),
  ADD CONSTRAINT adjustment_states_reason
    CHECK (adjusts_id IS NULL OR (reason IS NOT NULL AND length(trim(reason)) > 10)),
  ADD CONSTRAINT correction_is_reversal_or_adjustment
    CHECK (reverses_id IS NULL OR adjusts_id IS NULL);

CREATE FUNCTION ledger_correction_same_society() RETURNS TRIGGER AS $$
BEGIN
  IF coalesce(NEW.adjusts_id, NEW.reverses_id) IS NOT NULL
     AND (SELECT society_id FROM ledger_entries WHERE id = coalesce(NEW.adjusts_id, NEW.reverses_id))
         IS DISTINCT FROM NEW.society_id
     AND EXISTS (SELECT 1 FROM ledger_entries WHERE id = coalesce(NEW.adjusts_id, NEW.reverses_id)) THEN
    RAISE EXCEPTION 'a correction must be posted in the society of the entry it corrects'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'correction_same_society';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_entries_b_correction_same_society
  BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION ledger_correction_same_society();

-- =============================================================================
-- P — A CLOSED PERIOD IS READ-ONLY (§5)
--
-- NOT ENFORCED HERE, DELIBERATELY: "a close is refused if its trial balance
-- does not sum to zero". Every entry already balances at commit, so the
-- condition cannot arise; see the note in the tests.
-- =============================================================================

CREATE TABLE accounting_periods (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id UUID NOT NULL REFERENCES societies(id),
  starts_on  DATE NOT NULL,
  ends_on    DATE NOT NULL,
  closed_at  TIMESTAMPTZ,            -- NULL = open
  closed_by  UUID REFERENCES users(id),

  CONSTRAINT period_range_sane CHECK (ends_on >= starts_on),
  -- A date inside two periods could be both open and closed.
  CONSTRAINT accounting_periods_no_overlap
    EXCLUDE USING gist (society_id WITH =, daterange(starts_on, ends_on, '[]') WITH &&)
);

-- A close is final: no reopening, no moving its boundaries, no deleting it.
-- Otherwise "reopen, post, re-close" makes the close decorative.
CREATE FUNCTION period_close_is_final() RETURNS TRIGGER AS $$
BEGIN
  IF OLD.closed_at IS NOT NULL THEN
    RAISE EXCEPTION 'period %..% is closed; a closed period does not change', OLD.starts_on, OLD.ends_on
      USING ERRCODE = 'check_violation', CONSTRAINT = 'period_close_is_final';
  END IF;
  RETURN CASE TG_OP WHEN 'DELETE' THEN OLD ELSE NEW END;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER accounting_periods_close_is_final
  BEFORE UPDATE OR DELETE ON accounting_periods FOR EACH ROW EXECUTE FUNCTION period_close_is_final();

-- The date that matters is value_date — when the movement happened in the
-- society's books — not when it was keyed in. FOR SHARE makes a posting and a
-- concurrent close serialise: the close waits for the posting, or the posting
-- sees the close.
CREATE FUNCTION ledger_entry_not_in_closed_period() RETURNS TRIGGER AS $$
DECLARE
  closed TIMESTAMPTZ;
BEGIN
  IF NEW.value_date IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT p.closed_at INTO closed FROM accounting_periods p
   WHERE p.society_id = NEW.society_id AND NEW.value_date BETWEEN p.starts_on AND p.ends_on
   FOR SHARE;
  IF closed IS NOT NULL THEN
    RAISE EXCEPTION 'value date % falls in a closed period. Post an adjusting entry in the current period.', NEW.value_date
      USING ERRCODE = 'check_violation', CONSTRAINT = 'entry_not_in_closed_period';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_entries_c_not_in_closed_period
  BEFORE INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION ledger_entry_not_in_closed_period();

-- =============================================================================
-- G4 — THE INTERNAL AUDITOR HOLDS NO SIGNING POST, AS A PERSON
--
-- 0001's auditor_never_signs stops one route (an auditor appointment marked
-- signatory). This stops the others: the same member holding both, the same
-- PERSON holding both through two member records, a term extended into
-- overlap by UPDATE, and a member record RELINKED to a person who holds the
-- other post.
-- =============================================================================

-- Does this person (or, with no person, this member record) hold an
-- appointment of the given kind overlapping [from, until] in the society,
-- other than the one excluded?
CREATE FUNCTION person_holds_overlapping(
  p_society UUID, p_member UUID, p_person UUID, p_from DATE, p_until DATE,
  p_want_auditor BOOLEAN, p_exclude UUID
) RETURNS BOOLEAN AS $$
  SELECT EXISTS (
    SELECT 1 FROM appointments o JOIN members om ON om.id = o.member_id
     WHERE o.society_id = p_society
       AND o.id IS DISTINCT FROM p_exclude
       AND (o.member_id = p_member OR (p_person IS NOT NULL AND om.user_id = p_person))
       AND daterange(o.held_from, o.held_until, '[]') && daterange(p_from, p_until, '[]')
       AND CASE WHEN p_want_auditor THEN o.post = 'internal_auditor' ELSE o.is_signatory END)
$$ LANGUAGE sql STABLE;

CREATE FUNCTION appointment_auditor_holds_no_signing_post() RETURNS TRIGGER AS $$
DECLARE
  person UUID := (SELECT user_id FROM members WHERE id = NEW.member_id);
BEGIN
  PERFORM lock_person(person, NEW.member_id);
  IF (NEW.post = 'internal_auditor' AND person_holds_overlapping(NEW.society_id, NEW.member_id, person,
                                          NEW.held_from, NEW.held_until, false, NEW.id))
  OR (NEW.is_signatory AND person_holds_overlapping(NEW.society_id, NEW.member_id, person,
                                          NEW.held_from, NEW.held_until, true, NEW.id)) THEN
    RAISE EXCEPTION 'the internal auditor is read-only by Directorate rule and holds no signing post '
                    'over the same period, through any member record'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'auditor_holds_no_signing_post';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER appointments_auditor_holds_no_signing_post
  BEFORE INSERT OR UPDATE ON appointments FOR EACH ROW EXECUTE FUNCTION appointment_auditor_holds_no_signing_post();

-- Relinking a member record changes which person holds its appointments.
CREATE FUNCTION member_relink_auditor_check() RETURNS TRIGGER AS $$
DECLARE
  a appointments%ROWTYPE;
BEGIN
  IF NEW.user_id IS NOT DISTINCT FROM OLD.user_id OR NEW.user_id IS NULL THEN
    RETURN NEW;
  END IF;
  PERFORM lock_person(NEW.user_id, NEW.id);
  FOR a IN SELECT * FROM appointments WHERE member_id = NEW.id LOOP
    IF (a.post = 'internal_auditor' AND person_holds_overlapping(a.society_id, NEW.id, NEW.user_id,
                                          a.held_from, a.held_until, false, a.id))
    OR (a.is_signatory AND person_holds_overlapping(a.society_id, NEW.id, NEW.user_id,
                                          a.held_from, a.held_until, true, a.id)) THEN
      RAISE EXCEPTION 'relinking member % to user % would make one person both internal auditor and a signatory',
        NEW.id, NEW.user_id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'auditor_holds_no_signing_post';
    END IF;
  END LOOP;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER members_relink_auditor_check
  BEFORE UPDATE OF user_id ON members FOR EACH ROW EXECUTE FUNCTION member_relink_auditor_check();

-- =============================================================================
-- U, G3, S — SIGNATURES (PRD 8.4)
--
-- One signature per PERSON per request. The signer is derived from the member
-- record and SNAPSHOTTED (§4: frozen at signing) — member_id says which record
-- they acted under, user_id says who signed, and the two may diverge later
-- if the record is relinked. That divergence is required (test U4): they are
-- different facts, not one fact stored twice. Same shape as actor_post_label.
--
-- signed_at is the server's, like posted_at. The signer must, at that moment,
-- hold a current signing post in the request's society and not be the
-- internal auditor.
-- =============================================================================

ALTER TABLE signatory_approvals ADD COLUMN user_id UUID REFERENCES users(id);
ALTER TABLE signatory_approvals ALTER COLUMN user_id SET NOT NULL;

-- Replaces 0001's per-MEMBER key, which one person with two member records
-- satisfied twice. Per-member is implied: a member record has one user.
ALTER TABLE signatory_approvals DROP CONSTRAINT signatory_approvals_request_id_member_id_key;
ALTER TABLE signatory_approvals
  ADD CONSTRAINT signatory_approvals_one_per_user UNIQUE (request_id, user_id);

CREATE FUNCTION signature_admissible() RETURNS TRIGGER AS $$
DECLARE
  person  UUID;
  society UUID := (SELECT society_id FROM approval_requests WHERE id = NEW.request_id);
  today   DATE := society_today();
BEGIN
  IF NEW.signed_at IS DISTINCT FROM now() THEN
    RAISE EXCEPTION 'signed_at is written by the server, not supplied: got %', NEW.signed_at
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_approvals_signed_at_is_now';
  END IF;

  SELECT user_id INTO person FROM members WHERE id = NEW.member_id;
  IF NEW.user_id IS NOT NULL AND NEW.user_id IS DISTINCT FROM person THEN
    RAISE EXCEPTION 'member % belongs to user %, not %', NEW.member_id, person, NEW.user_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_user_is_member_user';
  END IF;
  IF person IS NULL THEN
    RAISE EXCEPTION 'member % is linked to no user; a signature is an act of a person', NEW.member_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_has_user';
  END IF;
  NEW.user_id := person;

  IF person_holds_overlapping(society, NEW.member_id, person, today, today, true, NULL) THEN
    RAISE EXCEPTION 'the internal auditor is read-only and does not sign'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_not_auditor';
  END IF;

  IF NOT person_holds_overlapping(society, NEW.member_id, person, today, today, false, NULL) THEN
    RAISE EXCEPTION 'user % holds no signing post in this society on %', person, today
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_holds_signing_post';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER signatory_approvals_admissible
  BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION signature_admissible();

COMMIT;
