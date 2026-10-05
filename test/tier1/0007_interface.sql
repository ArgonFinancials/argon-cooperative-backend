-- 0007's INTERFACE, with NO RULES: "M0" for suite 0007, approvals (PRD 8.4).
-- Applied on top of the real 0001–0006. M0 is TODAY'S BEHAVIOUR, made
-- callable: the status is whatever the writable column says (finding 14),
-- signing ignores expiry and rejection (finding 15), and a rejection is a row
-- that nothing checks.
--
--   approval_status(request, at)  THE status, as at a moment. Every consumer
--                                 (loans included) asks this, never the column.
--   approval_rejections           a rejection is its own ROW: who, in what
--                                 post, when, and one of Stage 5's four reasons
BEGIN;
CREATE TABLE approval_rejections (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id         UUID NOT NULL REFERENCES approval_requests(id),
  member_id          UUID NOT NULL REFERENCES members(id),
  user_id            UUID NOT NULL REFERENCES users(id),
  post_at_rejection  officer_post NOT NULL,
  -- Stage 5 (frontend lib/approvals.ts): four reasons, four different outcomes.
  --   documents_missing  back to the requester, KEEPING its place in the queue
  --   amount_wrong       back to the requester, to start again
  --   not_eligible       closed, recorded on the member's record
  --   looks_wrong        escalated to the committee and the internal auditor,
  --                      NOT back to the requester
  reason             TEXT NOT NULL,
  note               TEXT,
  rejected_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION approval_status(p_request UUID, p_at TIMESTAMPTZ DEFAULT now()) RETURNS approval_state AS $$
  SELECT state FROM approval_requests WHERE id = p_request;
$$ LANGUAGE sql STABLE;
COMMIT;
