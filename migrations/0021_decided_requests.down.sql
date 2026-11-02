BEGIN;
CREATE OR REPLACE FUNCTION signature_on_open_request() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r approval_requests%ROWTYPE;
BEGIN
  SELECT * INTO r FROM approval_requests WHERE id = NEW.request_id FOR UPDATE;
  IF NOT FOUND THEN
    RETURN NEW;
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
COMMIT;
