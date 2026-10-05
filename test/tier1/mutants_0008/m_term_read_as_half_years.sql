-- DELIBERATELY WRONG. The product's maximum is compared as if it were in half-years: a 24-month loan passes a 12-month product.
--
-- Must turn red: TestLoan3_TermWithinTheProductsMaximum.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.term_months > 2 * (SELECT max_term_months FROM loan_products WHERE id = NEW.product_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_term_within_product'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loans FOR EACH ROW EXECUTE FUNCTION m_f();
