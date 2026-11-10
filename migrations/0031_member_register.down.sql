-- 0031 down: the register's rules and records removed. Members registered
-- stay members; their attribution goes with the columns. Back up first.
BEGIN;
DROP FUNCTION register_member(UUID, TEXT, TEXT, TEXT, TEXT, DATE, collection_channel, TEXT, TEXT, TEXT, UUID, UUID, INT);
ALTER TABLE members DROP CONSTRAINT members_register_batch_fkey;
ALTER TABLE members DROP COLUMN register_line, DROP COLUMN register_batch_id, DROP COLUMN registered_by_act, DROP COLUMN next_of_kin_relationship, DROP COLUMN next_of_kin_phone, DROP COLUMN next_of_kin_name;
DROP TABLE member_register_batches;
COMMIT;
