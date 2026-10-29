-- 0019's INTERFACE: "M0" for suite 0019, the shape with NO RULES: the table a
-- send is recorded in, accepting anything. Applied on top of the real 0001-0018.
BEGIN;
CREATE TABLE sms_sends (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code_id             UUID NOT NULL REFERENCES auth_codes(id),
  provider            TEXT NOT NULL,
  outcome             TEXT NOT NULL,
  provider_message_id TEXT,
  provider_code       TEXT,
  at                  TIMESTAMPTZ NOT NULL DEFAULT auth_now()
);
COMMIT;
