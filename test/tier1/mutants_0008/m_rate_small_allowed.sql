-- DELIBERATELY WRONG. A 'small' rate is let through: only rates above 1% are refused. Any rate without an agreed method is invented policy.
--
-- Must turn red: TestLoan1_ProductRateIsZeroUntilAMethodIsAgreed.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.interest_rate_bp > 100 THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'interest_method_undecided'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT OR UPDATE ON loan_products FOR EACH ROW EXECUTE FUNCTION m_f();
