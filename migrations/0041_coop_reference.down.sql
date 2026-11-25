BEGIN;
ALTER TABLE member_bank_accounts DROP CONSTRAINT IF EXISTS bank_not_at_vfd;
CREATE OR REPLACE FUNCTION vfd_transfer_reference(p_hold UUID) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT 'Argon-' || replace(p_hold::text, '-', '')
$$;
COMMIT;
