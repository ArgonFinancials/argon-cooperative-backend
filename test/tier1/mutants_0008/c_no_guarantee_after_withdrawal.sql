-- DELIBERATELY WRONG. OVER-STRICT: once any guarantor withdraws, no one may guarantee the loan again.
--
-- Must turn red: TestLoan_Control_WithdrawnGuarantorReplaced.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM loan_guarantee_withdrawals w JOIN loan_guarantees g ON g.id = w.guarantee_id WHERE g.loan_id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantor_member_of_society'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON loan_guarantees FOR EACH ROW EXECUTE FUNCTION m_f();
