-- DELIBERATELY WRONG (over-strict). An account's role can never change, even
-- before any line uses it.
--
-- The claim: this breaks the NORMAL path. A society setting up its chart
-- will mislabel an account before using it, and correcting that harms
-- nothing because no history reads it yet. The rule is "final once USED".
--
-- Must turn red: TestRole_Control_UnusedAccountMayBeRelabelled.
CREATE FUNCTION m_role_final() RETURNS trigger AS $$
BEGIN
  IF NEW.role IS DISTINCT FROM OLD.role THEN
    -- (the error: no exception for an account that carries no lines)
    RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'account_role_is_final';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER m_role_final BEFORE UPDATE OF role ON accounts
  FOR EACH ROW EXECUTE FUNCTION m_role_final();
