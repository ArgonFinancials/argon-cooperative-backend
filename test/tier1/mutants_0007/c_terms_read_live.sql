-- DELIBERATELY WRONG. OVER-STRICT: the guarantor count is read from the product NOW, not from the loan's snapshot.
--
-- Must turn red: TestLoan_Control_ProductChangeLeavesAgreedLoansAlone.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.loan_id IS NOT NULL AND (SELECT count(*) FROM loan_guarantees g WHERE g.loan_id = NEW.loan_id
        AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals w WHERE w.guarantee_id = g.id))
     < (SELECT p.guarantors_required FROM loans l JOIN loan_products p ON p.id = l.product_id WHERE l.id = NEW.loan_id) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_requests FOR EACH ROW EXECUTE FUNCTION m_f();
