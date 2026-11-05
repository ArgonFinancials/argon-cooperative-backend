-- 0022's INTERFACE: "M0" for suite 0022, the lockout split by device. TODAY'S
-- BEHAVIOUR under the new name, so the tests show what is wrong with it:
--   lock_state_unknown_device(p_user)  the lock for sign-ins from a device that
--                                      is not one of the member's own: today,
--                                      the one shared lock (0003)
-- lock_state and lock_state_for_credential stay 0003's.
BEGIN;
CREATE FUNCTION lock_state_unknown_device(p_user UUID, OUT locked_until TIMESTAMPTZ, OUT needs_officer BOOLEAN) AS $$
  SELECT * FROM lock_state_of(p_user, NULL);
$$ LANGUAGE sql STABLE;
COMMIT;
