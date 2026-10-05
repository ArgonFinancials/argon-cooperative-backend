-- DELIBERATELY WRONG. OVER-STRICT: 'looks_wrong' is refused as not a reason, closing the fraud path.
--
-- Must turn red: TestApproval_Control_EachOfTheFourReasons.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  IF NEW.reason NOT IN ('documents_missing', 'amount_wrong', 'not_eligible') THEN RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'rejection_reason_known'; END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE INSERT ON approval_rejections FOR EACH ROW EXECUTE FUNCTION m_f();
