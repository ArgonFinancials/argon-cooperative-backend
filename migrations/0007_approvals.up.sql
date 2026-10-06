-- =============================================================================
-- 0007 — APPROVALS ARE A CONTROL, NOT A RECORD (PRD 8.4; findings 14 and 15)
--
-- Until now approval_requests.state was a free column: the API role could
-- UPDATE it, nothing tied it to signatures, and signing ignored expiry and
-- rejection. PRD 8.4's multi-signatory control rested on a column anything
-- could write. Fixed at the column, not in each reader (decided 2026-10-05):
--
--   - The status is DERIVED: approval_status(request, at). Nothing stores it.
--     A stored status is wrong the moment a request expires untouched, and
--     anything keeping it right would be a writer.
--   - A rejection is its own ROW: who, in what post, when (server time), and
--     one of Stage 5's four reasons. A status has no author, time or reason.
--   - Signing refuses an expired or rejected request; rejecting refuses an
--     approved or expired one, and a signatory who signed never rejects.
--   - A resubmission points at the rejected request it replaces.
--
-- Tests: test/tier1/tier1_0007_test.go (suite 0007).
-- =============================================================================
BEGIN;

-- ---- rejections: a row, attributable, immutable ---------------------------------
CREATE TABLE approval_rejections (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id         UUID NOT NULL REFERENCES approval_requests(id),
  member_id          UUID NOT NULL REFERENCES members(id),
  user_id            UUID NOT NULL REFERENCES users(id),
  post_at_rejection  officer_post NOT NULL,
  -- Stage 5 (frontend lib/approvals.ts): four reasons, four outcomes.
  reason             TEXT NOT NULL CONSTRAINT rejection_reason_known
                       CHECK (reason IN ('documents_missing', 'amount_wrong', 'not_eligible', 'looks_wrong')),
  note               TEXT,
  rejected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- The fraud path goes to the committee and the auditor, not back to the
  -- requester: it does not travel as a feeling nobody wrote down.
  CONSTRAINT rejection_note_for_looks_wrong
    CHECK (reason <> 'looks_wrong' OR length(trim(coalesce(note, ''))) > 0)
);
CREATE INDEX approval_rejections_request_idx ON approval_rejections (request_id);

-- ---- the status: derived, as at a moment -----------------------------------------
-- approved: a quorum of distinct people, each holding a signing post when they
--           signed (signature_admissible), signed before expiry. Once reached,
--           expiry does not undo it.
-- rejected: a rejection row exists (rejection is refused once approved).
-- expired:  the clock passed with neither.
ALTER TABLE approval_requests DROP COLUMN state;

CREATE FUNCTION approval_status(p_request UUID, p_at TIMESTAMPTZ DEFAULT now()) RETURNS approval_state AS $$
  SELECT (CASE
    WHEN EXISTS (SELECT 1 FROM approval_rejections j WHERE j.request_id = r.id AND j.rejected_at <= p_at) THEN 'rejected'
    WHEN (SELECT count(DISTINCT sa.user_id) FROM signatory_approvals sa
           WHERE sa.request_id = r.id AND sa.signed_at <= r.expires_at AND sa.signed_at <= p_at) >= r.required THEN 'approved'
    WHEN p_at > r.expires_at THEN 'expired'
    ELSE 'awaiting' END)::approval_state
  FROM approval_requests r WHERE r.id = p_request;
$$ LANGUAGE sql STABLE;

-- ---- signing: only an open request ------------------------------------------------
-- The request row is locked, so a signature and a rejection racing on one
-- request serialise: whichever commits second sees the first.
--
-- SECURITY DEFINER (as 0005's rules): a row lock needs UPDATE privilege, and
-- the app holds none on approval_requests (a request is never updated). The
-- lock is the rule's need, not the app's: without this the app could not sign
-- at all (finding 16; the probe caught it, the suite, run as owner, could not).
CREATE FUNCTION signature_on_open_request() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r approval_requests%ROWTYPE;
BEGIN
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id FOR UPDATE;
  IF NOT FOUND THEN
    RETURN NEW;   -- the foreign key refuses it, by name
  END IF;
  IF EXISTS (SELECT 1 FROM approval_rejections WHERE request_id = r.id) THEN
    RAISE EXCEPTION 'request % was rejected; it takes no more signatures', r.id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_on_open_request';
  END IF;
  IF now() > r.expires_at THEN
    RAISE EXCEPTION 'request % expired at %; a signature after expiry counts for nothing and is refused', r.id, r.expires_at
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_before_expiry';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER signatory_approvals_on_open_request
  BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION signature_on_open_request();

-- ---- rejecting: by a signatory who has not signed, on an open request -------------
CREATE FUNCTION rejection_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r      approval_requests%ROWTYPE;
  person UUID;
  today  DATE := society_today();
BEGIN
  IF NEW.rejected_at IS DISTINCT FROM now() THEN
    RAISE EXCEPTION 'rejected_at is written by the server, not supplied: got %', NEW.rejected_at
      USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_rejected_at_is_now';
  END IF;
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id FOR UPDATE;
  IF NOT FOUND THEN
    RETURN NEW;   -- the foreign key refuses it, by name
  END IF;

  SELECT user_id INTO person FROM members WHERE id = NEW.member_id;
  IF person IS NULL OR person IS DISTINCT FROM NEW.user_id THEN
    RAISE EXCEPTION 'member % belongs to user %, not %: a rejection is the act of the person on the record', NEW.member_id, person, NEW.user_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'rejecter_user_is_member_user';
  END IF;
  IF NOT person_holds_overlapping(r.society_id, NEW.member_id, person, today, today, false, NULL) THEN
    RAISE EXCEPTION 'user % holds no signing post in this society on %; only a signatory rejects', person, today
      USING ERRCODE = 'check_violation', CONSTRAINT = 'rejecter_holds_signing_post';
  END IF;
  IF approval_status(r.id) <> 'awaiting' THEN
    RAISE EXCEPTION 'request % is %; only an open request can be rejected', r.id, approval_status(r.id)
      USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_on_open_request';
  END IF;
  -- Decided 2026-10-05: a signatory who signed has answered; a signature and a
  -- rejection by one person would leave the status to arbitrate between them.
  IF EXISTS (SELECT 1 FROM signatory_approvals WHERE request_id = r.id AND user_id = person) THEN
    RAISE EXCEPTION 'user % signed request %; a signatory does not also reject it', person, r.id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'rejecter_has_not_signed';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER approval_rejections_admissible
  BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION rejection_admissible();

CREATE FUNCTION approval_rejections_immutable() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'a rejection is never altered or removed: % refused', TG_OP
    USING ERRCODE = 'check_violation', CONSTRAINT = 'approval_rejections_immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER approval_rejections_immutable
  BEFORE UPDATE OR DELETE ON approval_rejections FOR EACH ROW EXECUTE FUNCTION approval_rejections_immutable();

-- ---- a resubmission points at the rejected request it replaces ---------------------
-- Nothing reads it yet: the inbox will, so that "documents_missing" keeps its
-- place in the queue. Carried now; added later it would mean backfilling.
ALTER TABLE approval_requests ADD COLUMN resubmits_id UUID REFERENCES approval_requests(id);

CREATE FUNCTION resubmission_admissible() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.resubmits_id IS NOT NULL AND NOT EXISTS (
       SELECT 1 FROM approval_requests o JOIN approval_rejections j ON j.request_id = o.id
        WHERE o.id = NEW.resubmits_id AND o.society_id = NEW.society_id) THEN
    RAISE EXCEPTION 'a resubmission points at a REJECTED request of its own society; % is not one', NEW.resubmits_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'resubmits_a_rejected_request';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER approval_requests_resubmission
  BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION resubmission_admissible();

COMMIT;
