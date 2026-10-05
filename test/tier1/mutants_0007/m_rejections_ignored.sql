-- DELIBERATELY WRONG. The status ignores rejection rows: a rejected request reads 'awaiting'.
--
-- Must turn red: TestApproval7_RejectionClosesTheRequest.
ALTER TABLE approval_requests DROP COLUMN state;
CREATE OR REPLACE FUNCTION approval_status(p_request UUID, p_at TIMESTAMPTZ DEFAULT now()) RETURNS approval_state AS $$
  SELECT (CASE
    WHEN (SELECT count(DISTINCT sa.user_id) FROM signatory_approvals sa WHERE sa.request_id = r.id AND sa.signed_at <= r.expires_at AND sa.signed_at <= p_at) >= r.required THEN 'approved'
    WHEN p_at > r.expires_at THEN 'expired'
    ELSE 'awaiting' END)::approval_state
  FROM approval_requests r WHERE r.id = p_request;
$$ LANGUAGE sql STABLE;
