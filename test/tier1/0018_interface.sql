-- 0018's INTERFACE: "M0" for suite 0018, the shape with NO RULES: the two
-- functions exist and answer nothing. Applied on top of the real 0001-0017.
BEGIN;
CREATE FUNCTION member_guarantees_held(p_member UUID)
RETURNS TABLE (loan_id UUID, borrower_member_id UUID, borrower_name TEXT, owed_kobo BIGINT)
LANGUAGE sql STABLE AS $$ SELECT NULL::uuid, NULL::uuid, NULL::text, NULL::bigint WHERE false $$;
CREATE FUNCTION member_position(p_member UUID)
RETURNS TABLE (savings_kobo BIGINT, held_kobo BIGINT, floor_kobo BIGINT, open_holds_kobo BIGINT, withdrawable_kobo BIGINT)
LANGUAGE sql STABLE AS $$ SELECT NULL::bigint, NULL::bigint, NULL::bigint, NULL::bigint, NULL::bigint $$;
COMMIT;
