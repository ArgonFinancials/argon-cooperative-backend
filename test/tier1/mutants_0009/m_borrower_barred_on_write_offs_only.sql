-- DELIBERATELY WRONG. The borrower may not sign a write-off of their loan, but may sign its disbursement (finding 19, half fixed).
--
-- Must turn red: TestWriteOff6_TheBorrowerNeverSignsARequestForTheirOwnLoan.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN loans l ON l.id = r.loan_id JOIN members m ON m.id = l.member_id
              WHERE r.id = NEW.request_id AND r.kind::text IN ('loan_write_off') AND m.user_id = (SELECT user_id FROM members WHERE id = NEW.member_id)) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'borrower_never_signs_own_loan'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION m_f();
