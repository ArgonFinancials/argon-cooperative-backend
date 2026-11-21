BEGIN;
DROP FUNCTION operator_set_demo_pin(UUID, TEXT, TEXT, TEXT, TEXT);
DROP TABLE demo_pins;
DROP FUNCTION operator_create_demo_society(TEXT, TEXT, TEXT, TEXT, TEXT);
DROP FUNCTION auth_demo_only(UUID);
DROP TRIGGER society_demo_rule ON societies;
DROP FUNCTION society_demo_rule();
ALTER TABLE societies DROP COLUMN demo;
COMMIT;
