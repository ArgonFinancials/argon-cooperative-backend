-- DELIBERATELY WRONG. The product's society is checked; the borrower's is not.
--
-- Must turn red: TestLoan11_BorrowerIsAMemberOfTheLoansSociety.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF (SELECT society_id FROM loan_products WHERE id = NEW.product_id) <> NEW.society_id THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_product_in_society'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loans FOR EACH ROW EXECUTE FUNCTION m_f();
