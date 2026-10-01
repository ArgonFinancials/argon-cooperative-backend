-- DELIBERATELY WRONG. The signatory reset executes on the FIRST approval.
--
-- The claim: the two-signatory reset (D-15 revised) is only a 2-of-N act if
-- nothing happens until the second approval lands. Execute on the first,
-- and one officer resets a signatory alone. The request then just adds a
-- step to the single-officer reset that D-15 refuses.
--
-- Must turn red: TestAuthD1_OneApprovalDoesNotReset — which passes on M0
-- (no reset ever happens there), and so needs this mutant to prove it can
-- fail at all.
CREATE FUNCTION m_approve() RETURNS trigger AS $$
DECLARE
  rq credential_acts%ROWTYPE;
BEGIN
  IF NEW.kind <> 'signatory_approval' THEN
    RETURN NEW;
  END IF;
  SELECT * INTO rq FROM credential_acts WHERE id = NEW.request_id;
  IF rq.kind = 'signatory_reset_request' THEN
    -- (missing: wait for a second approval from another signatory)
    DELETE FROM auth_credentials WHERE user_id = rq.target_user_id;
    UPDATE auth_sessions SET ended_at = auth_now()
     WHERE user_id = rq.target_user_id AND ended_at IS NULL;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_approve AFTER INSERT ON credential_acts
  FOR EACH ROW EXECUTE FUNCTION m_approve();
