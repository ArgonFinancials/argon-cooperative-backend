-- DELIBERATELY WRONG. A guarantor cannot sign, but a signatory can still guarantee afterwards: the other direction of two.
--
-- Must turn red: TestLoan22_SignatoryCannotThenGuarantee.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN loan_guarantees g ON g.loan_id = r.loan_id
             WHERE r.id = NEW.request_id AND g.guarantor_user_id = (SELECT user_id FROM members WHERE id = NEW.member_id)) THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_not_guarantor'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON signatory_approvals FOR EACH ROW EXECUTE FUNCTION m_f();
