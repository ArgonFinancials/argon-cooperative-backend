-- 0015's INTERFACE: "M0" for suite 0015, the shape with NO RULES: the client's
-- reference on an entry, nothing checked. Applied on top of the real
-- 0001–0014.
BEGIN;
ALTER TABLE ledger_entries ADD COLUMN client_reference TEXT;
COMMIT;
