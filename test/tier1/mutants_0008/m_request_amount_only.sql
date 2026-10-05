-- DELIBERATELY WRONG. A loan's request must be for its principal; its member and kind are not checked.
--
-- Must turn red: TestLoan23_RequestIsForThisLoan.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.loan_id IS NOT NULL AND NEW.amount_kobo <> (SELECT principal_kobo FROM loans WHERE id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_request_matches_loan'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
