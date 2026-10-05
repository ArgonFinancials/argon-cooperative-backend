-- DELIBERATELY WRONG. The guarantor's member RECORD is compared with the borrower's, not the person.
--
-- Must turn red: TestLoan15_BorrowerNeverGuaranteesTheirOwnLoan.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.guarantor_member_id = (SELECT member_id FROM loans WHERE id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantor_not_borrower'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_guarantees FOR EACH ROW EXECUTE FUNCTION m_f();
