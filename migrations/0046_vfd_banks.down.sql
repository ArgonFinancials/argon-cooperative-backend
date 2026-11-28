BEGIN;
DROP VIEW IF EXISTS vfd_banks_current;
DROP TABLE IF EXISTS vfd_banks;
DROP TABLE IF EXISTS vfd_bank_lists;
DROP FUNCTION IF EXISTS vfd_banks_append_only();
DROP FUNCTION IF EXISTS vfd_bank_list_whole();
DROP FUNCTION IF EXISTS vfd_bank_lists_admissible();
COMMIT;
