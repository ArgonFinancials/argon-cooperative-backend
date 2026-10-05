-- DELIBERATELY WRONG. A signatory cannot then guarantee, but a guarantor can still sign: one direction of two.
--
-- Must turn red: TestLoan21_GuarantorCannotSignTheLoan.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN signatory_approvals sa ON sa.request_id = r.id WHERE r.loan_id = NEW.loan_id AND sa.user_id = NEW.guarantor_user_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_not_guarantor'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_guarantees FOR EACH ROW EXECUTE FUNCTION m_f();
