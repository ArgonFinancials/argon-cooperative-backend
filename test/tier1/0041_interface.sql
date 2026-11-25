-- 0041's INTERFACE: "M0" for suite 0041, GENERATED (gen0041.py) from the migration
-- with every rule off: the wallet's Argon- prefix, and bank details at VFD taken.
-- Applied on top of the real 0001-0040.
BEGIN;

CREATE OR REPLACE FUNCTION vfd_transfer_reference(p_hold UUID) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT 'Argon-' || replace(p_hold::text, '-', '')
$$;


COMMIT;
