-- DELIBERATELY WRONG. The borrower's society is checked; the product's is not.
--
-- Must turn red: TestLoan12_ProductIsTheLoansSocietys.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF (SELECT society_id FROM members WHERE id = NEW.member_id) <> NEW.society_id THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_borrower_in_society'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loans FOR EACH ROW EXECUTE FUNCTION m_f();
