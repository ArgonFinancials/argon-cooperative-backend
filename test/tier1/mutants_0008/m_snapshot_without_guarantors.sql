-- DELIBERATELY WRONG. The snapshot is compared for multiple, rate and prepayment, but not the guarantor count.
--
-- Must turn red: TestLoan2_LoanCarriesItsProductsTerms.
CREATE FUNCTION m_f() RETURNS trigger AS $$ DECLARE p loan_products%ROWTYPE; BEGIN
  SELECT * INTO p FROM loan_products WHERE id = NEW.product_id;
  IF NEW.savings_multiple_pct <> p.savings_multiple_pct OR NEW.interest_rate_bp <> p.interest_rate_bp OR NEW.allows_prepayment <> p.allows_prepayment THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_terms_are_products'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loans FOR EACH ROW EXECUTE FUNCTION m_f();
