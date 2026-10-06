-- =============================================================================
-- 0008 — THE LOAN BOOK (LOAN_SHAPE.md, approved 2026-10-05)
--
-- The LOAN holds what was agreed; the LEDGER holds what moved. There is no
-- outstanding-balance column: what is owed is derived from the ledger, always.
--
--   - Terms are the product's, snapshotted at agreement. The rate is product
--     configuration, refused above zero until a method of applying it is
--     agreed (interest_method_undecided).
--   - The agreement and its schedule never change; a reschedule is a new loan.
--   - A guarantor consents only by their OWN step-up act; a signatory never
--     guarantees and a guarantor never signs, in either order.
--   - Approval is approval_status (0007), never a column. The guarantor count
--     is checked when the request is raised AND again at disbursement.
--
-- Every rule function is SECURITY DEFINER with a pinned search_path: the
-- rules read tables the app may not read in full (credential_acts) and lock
-- the loan row so a withdrawal and a disbursement cannot race. Those are the
-- rules' needs, not the app's (finding 16).
--
-- Tests: test/tier1/tier1_0008_test.go (suite 0008).
-- =============================================================================
BEGIN;

-- ---- products: terms are configuration ------------------------------------------
CREATE TABLE loan_products (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id           UUID NOT NULL REFERENCES societies(id),
  code                 TEXT NOT NULL,
  name                 TEXT NOT NULL,
  max_term_months      SMALLINT NOT NULL CONSTRAINT product_term_positive CHECK (max_term_months > 0),
  guarantors_required  SMALLINT NOT NULL CONSTRAINT product_guarantors_sane CHECK (guarantors_required BETWEEN 0 AND 9),
  savings_multiple_pct INTEGER NOT NULL CONSTRAINT product_multiple_positive CHECK (savings_multiple_pct > 0),
  -- Basis points, an integer. Configuration, not an assumption in the code;
  -- the only value accepted until a society agrees how interest is applied
  -- is 0. When it is agreed, this constraint is replaced by the method.
  interest_rate_bp     INTEGER NOT NULL DEFAULT 0 CONSTRAINT interest_method_undecided CHECK (interest_rate_bp = 0),
  allows_prepayment    BOOLEAN NOT NULL,
  UNIQUE (society_id, code)
);

-- ---- the agreement --------------------------------------------------------------
CREATE TABLE loans (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  society_id           UUID NOT NULL REFERENCES societies(id),
  member_id            UUID NOT NULL REFERENCES members(id),
  product_id           UUID NOT NULL REFERENCES loan_products(id),
  principal_kobo       BIGINT NOT NULL CONSTRAINT loan_principal_positive CHECK (principal_kobo > 0),
  term_months          SMALLINT NOT NULL CONSTRAINT loan_term_positive CHECK (term_months > 0),
  purpose              TEXT NOT NULL,
  guarantors_required  SMALLINT NOT NULL,
  savings_multiple_pct INTEGER NOT NULL,
  interest_rate_bp     INTEGER NOT NULL,
  allows_prepayment    BOOLEAN NOT NULL,
  supersedes_id        UUID REFERENCES loans(id),
  agreed_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  agreed_xid           xid8 NOT NULL DEFAULT pg_current_xact_id()
);
CREATE INDEX loans_member_idx ON loans (member_id);
-- The disbursement request names its loan. (Added here: the guarantor rules
-- below read it, and a SQL function is checked when it is created.)
ALTER TABLE approval_requests ADD COLUMN loan_id UUID REFERENCES loans(id);

CREATE TABLE loan_instalments (
  loan_id     UUID NOT NULL REFERENCES loans(id),
  n           SMALLINT NOT NULL,
  due_on      DATE NOT NULL,
  amount_kobo BIGINT NOT NULL CONSTRAINT instalment_amount_positive CHECK (amount_kobo > 0),
  PRIMARY KEY (loan_id, n)
);

CREATE FUNCTION loan_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  p       loan_products%ROWTYPE;
  savings BIGINT;
BEGIN
  IF (SELECT society_id FROM members WHERE id = NEW.member_id) IS DISTINCT FROM NEW.society_id THEN
    RAISE EXCEPTION 'the borrower is not a member of the loan''s society'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_borrower_in_society';
  END IF;
  SELECT * INTO p FROM loan_products WHERE id = NEW.product_id;
  IF p.society_id IS DISTINCT FROM NEW.society_id THEN
    RAISE EXCEPTION 'the product is not the loan''s society''s'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_product_in_society';
  END IF;
  IF (NEW.guarantors_required, NEW.savings_multiple_pct, NEW.interest_rate_bp, NEW.allows_prepayment)
     IS DISTINCT FROM (p.guarantors_required, p.savings_multiple_pct, p.interest_rate_bp, p.allows_prepayment) THEN
    RAISE EXCEPTION 'a loan carries its product''s terms as they are at agreement'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_terms_are_products';
  END IF;
  IF NEW.term_months > p.max_term_months THEN
    RAISE EXCEPTION 'a %-month loan on a product of at most % months', NEW.term_months, p.max_term_months
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_term_within_product';
  END IF;
  -- The member's savings balance at agreement (a condition of the agreement,
  -- not re-checked later). Integer arithmetic only.
  SELECT -coalesce(sum(l.amount_kobo), 0) INTO savings
    FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
   WHERE a.role = 'member_savings' AND l.member_id = NEW.member_id;
  IF NEW.principal_kobo * 100 > savings * NEW.savings_multiple_pct THEN
    RAISE EXCEPTION 'principal % kobo exceeds % percent of savings % kobo', NEW.principal_kobo, NEW.savings_multiple_pct, savings
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_within_savings_multiple';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER loans_admissible BEFORE INSERT ON loans FOR EACH ROW EXECUTE FUNCTION loan_admissible();

CREATE FUNCTION loan_agreement_immutable() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'an agreed loan is never altered or removed: % refused. A reschedule is a new loan.', TG_OP
    USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_agreement_immutable';
END $$ LANGUAGE plpgsql;
CREATE TRIGGER loans_immutable BEFORE UPDATE OR DELETE ON loans FOR EACH ROW EXECUTE FUNCTION loan_agreement_immutable();

-- The schedule: written with the agreement, in its transaction, and never after.
CREATE FUNCTION loan_schedule_closed() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF (SELECT agreed_xid FROM loans WHERE id = NEW.loan_id) IS DISTINCT FROM pg_current_xact_id() THEN
    RAISE EXCEPTION 'the schedule of loan % was fixed at agreement; it does not grow', NEW.loan_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_closed';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER loan_instalments_closed BEFORE INSERT ON loan_instalments FOR EACH ROW EXECUTE FUNCTION loan_schedule_closed();

CREATE FUNCTION loan_schedule_immutable() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'an agreed instalment is never altered or removed: % refused', TG_OP
    USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_immutable';
END $$ LANGUAGE plpgsql;
CREATE TRIGGER loan_instalments_immutable BEFORE UPDATE OR DELETE ON loan_instalments
  FOR EACH ROW EXECUTE FUNCTION loan_schedule_immutable();

-- At commit: one instalment per month of term, numbered 1..term, due dates
-- increasing with the number, summing to the principal. Fires for the loan
-- row too, so a loan with no schedule at all is refused.
CREATE FUNCTION loan_schedule_whole() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  lid UUID;
  l   loans%ROWTYPE;
BEGIN
  -- An IF, not a CASE: PL/pgSQL resolves every NEW field an expression names,
  -- and a loans row has no loan_id.
  IF TG_TABLE_NAME = 'loans' THEN
    lid := NEW.id;
  ELSE
    lid := NEW.loan_id;
  END IF;
  SELECT * INTO l FROM loans WHERE id = lid;
  IF (SELECT count(*) FROM loan_instalments WHERE loan_id = lid) <> l.term_months
     OR EXISTS (SELECT 1 FROM loan_instalments WHERE loan_id = lid AND (n < 1 OR n > l.term_months)) THEN
    RAISE EXCEPTION 'loan % of % months has a schedule of % instalments', lid, l.term_months,
                    (SELECT count(*) FROM loan_instalments WHERE loan_id = lid)
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_matches_term';
  END IF;
  IF (SELECT sum(amount_kobo) FROM loan_instalments WHERE loan_id = lid) <> l.principal_kobo THEN
    RAISE EXCEPTION 'loan % schedule sums to % kobo, not the principal %', lid,
                    (SELECT sum(amount_kobo) FROM loan_instalments WHERE loan_id = lid), l.principal_kobo
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_sums_to_principal';
  END IF;
  IF EXISTS (SELECT 1 FROM loan_instalments a JOIN loan_instalments b ON b.loan_id = a.loan_id AND b.n = a.n + 1
              WHERE a.loan_id = lid AND b.due_on <= a.due_on) THEN
    RAISE EXCEPTION 'loan % has an instalment falling due no later than the one before it', lid
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_schedule_in_order';
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER loans_schedule_whole AFTER INSERT ON loans
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION loan_schedule_whole();
CREATE CONSTRAINT TRIGGER loan_instalments_whole AFTER INSERT ON loan_instalments
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION loan_schedule_whole();

-- ---- guarantors: a personal pledge, by their own step-up -------------------------
ALTER TABLE credential_acts DROP CONSTRAINT credential_acts_kind_check;
ALTER TABLE credential_acts ADD CONSTRAINT credential_acts_kind_check CHECK (kind IN ('pin_change', 'member_reset',
  'phone_change', 'lock_release', 'signatory_reset_request', 'signatory_phone_request', 'signatory_approval',
  'ledger_batch', 'guarantee', 'guarantee_withdrawal'));
ALTER TABLE auth_proofs DROP CONSTRAINT proof_act_kind_known;
ALTER TABLE auth_proofs ADD CONSTRAINT proof_act_kind_known CHECK (act_kind IS NULL OR act_kind IN (
  'ledger_batch', 'pin_change', 'member_reset', 'phone_change', 'lock_release',
  'signatory_reset_request', 'signatory_phone_request', 'signatory_approval', 'guarantee', 'guarantee_withdrawal'));

CREATE TABLE loan_guarantees (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  loan_id             UUID NOT NULL REFERENCES loans(id),
  guarantor_member_id UUID NOT NULL REFERENCES members(id),
  guarantor_user_id   UUID NOT NULL REFERENCES users(id),
  act_id              UUID NOT NULL REFERENCES credential_acts(id),
  at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Per PERSON, not per member record (signatory_approvals' lesson).
CREATE UNIQUE INDEX loan_guarantees_one_per_person ON loan_guarantees (loan_id, guarantor_user_id);

CREATE TABLE loan_guarantee_withdrawals (
  guarantee_id UUID PRIMARY KEY REFERENCES loan_guarantees(id),
  act_id       UUID NOT NULL REFERENCES credential_acts(id),
  at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Has this person signed any request for this loan?
CREATE FUNCTION loan_signed_by(p_loan UUID, p_user UUID) RETURNS BOOLEAN
SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM approval_requests r JOIN signatory_approvals sa ON sa.request_id = r.id
                  WHERE r.loan_id = p_loan AND sa.user_id = p_user);
$$ LANGUAGE sql STABLE;

-- Current (un-withdrawn) guarantees of a loan.
CREATE FUNCTION loan_current_guarantees(p_loan UUID) RETURNS BIGINT
SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT count(*) FROM loan_guarantees g
   WHERE g.loan_id = p_loan AND NOT EXISTS (SELECT 1 FROM loan_guarantee_withdrawals w WHERE w.guarantee_id = g.id);
$$ LANGUAGE sql STABLE;

CREATE FUNCTION loan_guarantee_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l     loans%ROWTYPE;
  act   credential_acts%ROWTYPE;
  m     members%ROWTYPE;
BEGIN
  SELECT * INTO l FROM loans WHERE id = NEW.loan_id FOR UPDATE;
  SELECT * INTO act FROM credential_acts WHERE id = NEW.act_id;
  -- Consent by proxy is theatre: the act is the guarantor's own.
  IF act.actor_user_id IS DISTINCT FROM NEW.guarantor_user_id THEN
    RAISE EXCEPTION 'a guarantee rests on the guarantor''s own step-up, not someone else''s'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantee_is_own_act';
  END IF;
  IF act.kind IS DISTINCT FROM 'guarantee' THEN
    RAISE EXCEPTION 'a guarantee rests on a guarantee act, not %', act.kind
      USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantee_act_is_guarantee';
  END IF;
  SELECT * INTO m FROM members WHERE id = NEW.guarantor_member_id;
  IF m.user_id IS DISTINCT FROM NEW.guarantor_user_id THEN
    RAISE EXCEPTION 'member % belongs to user %, not %', m.id, m.user_id, NEW.guarantor_user_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantor_user_is_member_user';
  END IF;
  IF NEW.guarantor_user_id = (SELECT user_id FROM members WHERE id = l.member_id) THEN
    RAISE EXCEPTION 'the borrower never guarantees their own loan, through any member record'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantor_not_borrower';
  END IF;
  IF m.society_id IS DISTINCT FROM l.society_id OR m.status <> 'active' THEN
    RAISE EXCEPTION 'a guarantor is an active member of the borrower''s society'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantor_member_of_society';
  END IF;
  IF loan_signed_by(l.id, NEW.guarantor_user_id) THEN
    RAISE EXCEPTION 'a signatory on a loan never guarantees it'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_not_guarantor';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER loan_guarantees_admissible BEFORE INSERT ON loan_guarantees
  FOR EACH ROW EXECUTE FUNCTION loan_guarantee_admissible();

CREATE FUNCTION loan_guarantee_withdrawal_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  g   loan_guarantees%ROWTYPE;
  act credential_acts%ROWTYPE;
BEGIN
  SELECT * INTO g FROM loan_guarantees WHERE id = NEW.guarantee_id;
  PERFORM 1 FROM loans WHERE id = g.loan_id FOR UPDATE;   -- serialise with a disbursement
  SELECT * INTO act FROM credential_acts WHERE id = NEW.act_id;
  IF act.actor_user_id IS DISTINCT FROM g.guarantor_user_id OR act.kind IS DISTINCT FROM 'guarantee_withdrawal' THEN
    RAISE EXCEPTION 'only the guarantor withdraws their guarantee, by their own step-up'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantee_withdrawal_is_own_act';
  END IF;
  IF EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = g.loan_id AND kind = 'loan_disbursement') THEN
    RAISE EXCEPTION 'the money has gone out: a guarantee is withdrawn before disbursement or not at all'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'guarantee_withdrawn_before_disbursement';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER loan_guarantee_withdrawals_admissible BEFORE INSERT ON loan_guarantee_withdrawals
  FOR EACH ROW EXECUTE FUNCTION loan_guarantee_withdrawal_admissible();

CREATE FUNCTION loan_guarantees_immutable() RETURNS TRIGGER AS $$
BEGIN
  RAISE EXCEPTION 'a guarantee or its withdrawal is never altered or removed: % refused', TG_OP
    USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantees_immutable';
END $$ LANGUAGE plpgsql;
CREATE TRIGGER loan_guarantees_immutable BEFORE UPDATE OR DELETE ON loan_guarantees
  FOR EACH ROW EXECUTE FUNCTION loan_guarantees_immutable();
CREATE TRIGGER loan_guarantee_withdrawals_immutable BEFORE UPDATE OR DELETE ON loan_guarantee_withdrawals
  FOR EACH ROW EXECUTE FUNCTION loan_guarantees_immutable();

-- ---- approval: the request is for this loan, and its guarantors are in place -----

CREATE FUNCTION loan_request_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l loans%ROWTYPE;
BEGIN
  IF NEW.loan_id IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO l FROM loans WHERE id = NEW.loan_id FOR UPDATE;
  IF NEW.kind <> 'loan_disbursement' OR NEW.amount_kobo <> l.principal_kobo
     OR NEW.member_id IS DISTINCT FROM l.member_id OR NEW.society_id <> l.society_id THEN
    RAISE EXCEPTION 'a loan''s request is its disbursement: the principal, to the borrower, in its society'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_request_matches_loan';
  END IF;
  IF loan_current_guarantees(l.id) < l.guarantors_required THEN
    RAISE EXCEPTION 'loan % has % current guarantees of the % agreed', l.id, loan_current_guarantees(l.id), l.guarantors_required
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER approval_requests_loan BEFORE INSERT ON approval_requests
  FOR EACH ROW EXECUTE FUNCTION loan_request_admissible();

-- A guarantor never signs the loan they guarantee.
CREATE FUNCTION signature_not_guarantor() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM approval_requests r JOIN loan_guarantees g ON g.loan_id = r.loan_id
              WHERE r.id = NEW.request_id AND g.guarantor_user_id = (SELECT user_id FROM members WHERE id = NEW.member_id)) THEN
    RAISE EXCEPTION 'a guarantor of a loan never signs it'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'signatory_not_guarantor';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER signatory_approvals_not_guarantor BEFORE INSERT ON signatory_approvals
  FOR EACH ROW EXECUTE FUNCTION signature_not_guarantor();

-- ---- the ledger boundary --------------------------------------------------------
ALTER TABLE ledger_entries ADD COLUMN loan_id UUID REFERENCES loans(id);
-- One disbursement per loan, race-proof.
CREATE UNIQUE INDEX loan_disbursed_once ON ledger_entries (loan_id) WHERE kind = 'loan_disbursement';

CREATE FUNCTION ledger_entry_loan_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l loans%ROWTYPE;
BEGIN
  IF NEW.kind NOT IN ('loan_disbursement', 'loan_repayment') THEN
    IF NEW.loan_id IS NOT NULL THEN
      RAISE EXCEPTION 'only a loan disbursement or repayment names a loan, not %', NEW.kind
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_id_only_on_loan_movements';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.loan_id IS NULL THEN
    RAISE EXCEPTION 'a % names the loan it moves money for', NEW.kind
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_movement_names_loan';
  END IF;
  SELECT * INTO l FROM loans WHERE id = NEW.loan_id FOR UPDATE;   -- serialise with a withdrawal
  IF l.society_id IS DISTINCT FROM NEW.society_id THEN
    RAISE EXCEPTION 'loan % is not this society''s', NEW.loan_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_same_society';
  END IF;
  IF NEW.kind = 'loan_disbursement' THEN
    IF NOT EXISTS (SELECT 1 FROM approval_requests r WHERE r.loan_id = l.id AND r.kind = 'loan_disbursement'
                     AND approval_status(r.id) = 'approved') THEN
      RAISE EXCEPTION 'loan % has no approved disbursement request', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_disbursement_approved';
    END IF;
    -- Re-checked here, not only at the request (decided 2026-10-05).
    IF loan_current_guarantees(l.id) < l.guarantors_required THEN
      RAISE EXCEPTION 'loan % has % current guarantees of the % agreed', l.id, loan_current_guarantees(l.id), l.guarantors_required
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_guarantors_met';
    END IF;
  ELSE
    IF NOT EXISTS (SELECT 1 FROM ledger_entries WHERE loan_id = l.id AND kind = 'loan_disbursement') THEN
      RAISE EXCEPTION 'loan % was never disbursed; there is nothing to repay', l.id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_repaid_after_disbursement';
    END IF;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER ledger_entries_d_loan BEFORE INSERT ON ledger_entries
  FOR EACH ROW EXECUTE FUNCTION ledger_entry_loan_admissible();

-- A loan movement's member_loan lines are the borrower's.
CREATE FUNCTION ledger_line_loan_admissible() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  e ledger_entries%ROWTYPE;
BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.loan_id IS NOT NULL AND (SELECT role FROM accounts WHERE id = NEW.account_id) = 'member_loan'
     AND NEW.member_id IS DISTINCT FROM (SELECT member_id FROM loans WHERE id = e.loan_id) THEN
    RAISE EXCEPTION 'loan % is the borrower''s: its loan lines name the borrower', e.loan_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_line_is_borrowers';
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER ledger_lines_loan BEFORE INSERT ON ledger_lines
  FOR EACH ROW EXECUTE FUNCTION ledger_line_loan_admissible();

-- At commit: a disbursement debits the borrower's loan by exactly the principal.
CREATE FUNCTION loan_disbursement_is_principal() RETURNS TRIGGER
SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  e ledger_entries%ROWTYPE;
BEGIN
  SELECT * INTO e FROM ledger_entries WHERE id = NEW.entry_id;
  IF e.kind = 'loan_disbursement' AND
     (SELECT coalesce(sum(l.amount_kobo), 0) FROM ledger_lines l JOIN accounts a ON a.id = l.account_id
       WHERE l.entry_id = e.id AND a.role = 'member_loan') IS DISTINCT FROM (SELECT principal_kobo FROM loans WHERE id = e.loan_id) THEN
    RAISE EXCEPTION 'a disbursement of loan % moves exactly its principal onto the borrower''s loan', e.loan_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'loan_disbursement_is_principal';
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER ledger_lines_loan_principal AFTER INSERT ON ledger_lines
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION loan_disbursement_is_principal();

COMMIT;
