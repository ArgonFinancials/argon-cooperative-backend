-- DELIBERATELY WRONG. UPDATE is refused; DELETE is not, so an agreed loan can vanish.
--
-- Must turn red: TestLoan5_AgreementIsImmutable.
CREATE FUNCTION m_f() RETURNS trigger AS $$ BEGIN
  RAISE EXCEPTION 'mutant' USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_agreement_immutable';
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER m_t BEFORE UPDATE ON loans FOR EACH ROW EXECUTE FUNCTION m_f();
