-- DELIBERATELY WRONG (over-strict). The signature that REACHES the quorum is refused: no request could ever be approved.
--
-- Must turn red: TestDecided02_Control_TheDecidingSignatureLands.
BEGIN;

CREATE OR REPLACE FUNCTION signature_on_open_request() RETURNS TRIGGER
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
  IF (SELECT count(DISTINCT sa.user_id) FROM signatory_approvals sa WHERE sa.request_id = r.id) >= r.required - 1 THEN
    RAISE EXCEPTION 'request % already has its % signatures: it is decided, and a further signature would be on nothing', r.id, r.required
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signature_on_decided_request';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMIT;
