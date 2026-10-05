-- DELIBERATELY WRONG. The status is derived correctly, but the old column is left in place, writable, for something to read later.
--
-- Must turn red: TestApproval1_StatusCannotBeWritten.
CREATE OR REPLACE FUNCTION approval_status(p_request UUID, p_at TIMESTAMPTZ DEFAULT now()) RETURNS approval_state AS $$
  SELECT (CASE
    WHEN EXISTS (SELECT 1 FROM approval_rejections j WHERE j.request_id = r.id AND j.rejected_at <= p_at) THEN 'rejected'
    WHEN (SELECT count(DISTINCT sa.user_id) FROM signatory_approvals sa WHERE sa.request_id = r.id AND sa.signed_at <= r.expires_at AND sa.signed_at <= p_at) >= r.required THEN 'approved'
    WHEN p_at > r.expires_at THEN 'expired'
    ELSE 'awaiting' END)::approval_state
  FROM approval_requests r WHERE r.id = p_request;
$$ LANGUAGE sql STABLE;
