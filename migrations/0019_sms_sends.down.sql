-- Reverses 0019. The records go with it: who was sent which code, and what
-- Termii answered, is lost. Nothing else depends on the table.
BEGIN;
DROP TABLE sms_sends;
DROP FUNCTION sms_sends_final();
DROP FUNCTION sms_sends_admissible();
COMMIT;
