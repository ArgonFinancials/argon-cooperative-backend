-- 0023's INTERFACE: "M0" for suite 0023. The standard chart (its reviewed
-- CONTENT, which is not a rule) and the operator's founding function with NO
-- RULES: it inserts the society and nothing else (no checks, no chart, no
-- quorums, no branch, no log). Applied on top of the real 0001–0022.
-- Callable only by the owner's role (the operator's); never by the app.
BEGIN;
CREATE TABLE standard_chart (
  code TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind account_kind NOT NULL,
  role TEXT NOT NULL UNIQUE
);
INSERT INTO standard_chart (code, name, kind, role) VALUES
  ('1000', 'Bank',                    'asset',     'bank'),
  ('1201', 'Member loans',            'asset',     'member_loan'),
  ('2000', 'Member savings',          'liability', 'member_savings'),
  ('2101', 'Partner charges payable', 'liability', 'charges_partner'),
  ('2102', 'VAT payable',             'liability', 'charges_vat'),
  ('2103', 'Statutory levy payable',  'liability', 'statutory_levy_payable'),
  ('2111', 'Argon charges payable',   'liability', 'charges_margin'),
  ('2201', 'Member wallets',          'liability', 'member_wallet'),
  ('3901', 'Migration control',       'equity',    'migration_control'),
  ('4901', 'Loan recoveries',         'income',    'loan_recovery_income'),
  ('5901', 'Loan losses',             'expense',   'loan_loss');

CREATE FUNCTION operator_create_society(p_code TEXT, p_name TEXT, p_reg TEXT, p_directorate TEXT,
    p_withdrawal INT, p_write_off INT, p_signatures INT,
    p_operator TEXT, p_login TEXT, p_ref TEXT, p_date DATE, p_reason TEXT)
  RETURNS uuid SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id UUID;
BEGIN
  INSERT INTO societies (code, registered_name, registration_number, state_directorate)
    VALUES (p_code, p_name, p_reg, p_directorate) RETURNING id INTO v_id;
  RETURN v_id;
END $$ LANGUAGE plpgsql;
REVOKE EXECUTE ON FUNCTION operator_create_society(TEXT, TEXT, TEXT, TEXT, INT, INT, INT, TEXT, TEXT, TEXT, DATE, TEXT) FROM PUBLIC;
COMMIT;
