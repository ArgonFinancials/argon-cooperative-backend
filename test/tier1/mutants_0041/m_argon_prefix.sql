-- DELIBERATELY WRONG (m_argon_prefix). GENERATED (gen0041.py): a full copy of 0041 with
-- one rule broken, applied over 0041_interface.sql, whose objects are removed first.
-- the interface's objects removed first (the down migration's body).
BEGIN;
ALTER TABLE member_bank_accounts DROP CONSTRAINT IF EXISTS bank_not_at_vfd;
CREATE OR REPLACE FUNCTION vfd_transfer_reference(p_hold UUID) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT 'Argon-' || replace(p_hold::text, '-', '')
$$;
COMMIT;

BEGIN;

CREATE OR REPLACE FUNCTION vfd_transfer_reference(p_hold UUID) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT 'Argon-' || replace(p_hold::text, '-', '')
$$;

ALTER TABLE member_bank_accounts ADD CONSTRAINT bank_not_at_vfd CHECK (bank_code <> '090110');

COMMIT;
