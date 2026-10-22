#!/usr/bin/env bash
# Tier 1 probe. Tries to break the database's own constraints and reports
# whether the database refused.
#
#   70-verify-tier1.sh              build a scratch argon_verify_<ts> from the
#                                   migrations, probe it, drop it
#   70-verify-tier1.sh --db NAME    probe an existing scratch/restored copy
#   70-verify-tier1.sh --keep       keep the scratch database afterwards
#
# NEVER point --db at the live database: probe fixtures land in ledger tables,
# and ledger rows cannot be deleted. That is the point.
#
# A case counts as REFUSED only when the SPECIFIC expected error appears (by
# SQLSTATE + message/constraint). A refusal for any other reason — a typo, a
# foreign key, a missing fixture — is WRONG ERROR, not a pass. Each refusal
# case has a CONTROL that must succeed, proving the statement shape is valid
# and only the constraint under test stops it.
#
# Exit 1 if any REQUIRED case is not refused as expected. GAP cases are
# reported (they probe edges of the constraints) but do not fail the run.
source "$(dirname "$0")/lib.sh"
require_env ARGON_APP_PASSWORD ARGON_MIGRATOR_PASSWORD

db=""; keep=0
while (($#)); do case $1 in
  --db) db=$2; shift 2 ;;
  --keep) keep=1; shift ;;
  *) die "unknown arg $1" ;;
esac; done
[[ -z $db ]] || assert_scratch "$db"

if [[ -z $db ]]; then
  db="argon_verify_$(date -u +%Y%m%dt%H%M%S)_$$"   # and the process: two runs in one second (finding 38)
  info "building scratch database $db from $MIGRATIONS_DIR"
  create_scratch_db "$db"
  ((keep)) || trap 'drop_scratch_db "$db" >/dev/null || true' EXIT
  run_migrate "$db" up >&2
  apply_grants "$db"
fi

# ---- fixtures (as argon_app, one transaction) --------------------------------
U() { printf "'a0000000-0000-4000-8000-%012d'" "$1"; }
S=$(U 1) B=$(U 2) M1=$(U 11) M2=$(U 12) M3=$(U 13) A1=$(U 21) A2=$(U 22)
BT=$(U 31) E1=$(U 41) EBAD=$(U 42) EOK=$(U 43) EZERO=$(U 44) L1=$(U 51) L2=$(U 52)
R1=$(U 61) LP=$(U 101) LN=$(U 102) ACTOR=$(U 91)
R3=$(U 63) E2=$(U 45) E3=$(U 46) E4=$(U 47) A3=$(U 23) A4=$(U 24)
SP6=$(U 116) SP7=$(U 117) SP8=$(U 118) SP9=$(U 119) SP10=$(U 120) SA6=$(U 126) SA7=$(U 127) SA8=$(U 128) SA9=$(U 129) SA10=$(U 130)
E0=$(U 40) L3=$(U 53) L4=$(U 54) RK45=$(U 64)
SPW1=$(U 161) SPW2=$(U 162) SPW3=$(U 163) SPW4=$(U 164) SPW5=$(U 165) SPW6=$(U 166) SPW7=$(U 167) SPW8=$(U 168) SAW8=$(U 178) RK47=$(U 66)
SAW1=$(U 171) SAW2=$(U 172) SAW3=$(U 173) SAW4=$(U 174) SAW5=$(U 175) SAW6=$(U 176) SAW7=$(U 177)
R2=$(U 62) SP5=$(U 115) SA5=$(U 125) SP1=$(U 111) SP2=$(U 112) SP3=$(U 113) SP4=$(U 114) SA1=$(U 121) SA2=$(U 122) SA3=$(U 123) SA4=$(U 124) U1=$(U 81) U2=$(U 82) U3=$(U 83) PROOF=$(U 71) ACT=$(U 72)

# Written for the schema at migration 0005 (users, line-level member_id, no
# ledger_batches.as_of; account roles, so every savings line names its member;
# a batch authorised by its actor's step-up act). M1 and M3 hold current
# signing posts; M2 is auditor. Adapts to a schema before 0005 (a restored
# older copy), where a batch names no act.
info "seeding fixtures in $db as argon_app"
pg_as argon_app "$db" -q -c "INSERT INTO users (id, full_name) VALUES
  ($ACTOR, 'Probe Actor'), ($U1, 'Probe Treasurer'), ($U2, 'Probe Auditor'), ($U3, 'Probe Chairman');" </dev/null >/dev/null
has_acts=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM information_schema.columns
  WHERE table_name = 'ledger_batches' AND column_name = 'authorised_by'" </dev/null)
if [[ $has_acts == 1 ]]; then
  # The batch's step-up: the proof is minted by argon_auth (the only role that
  # can) in its own transaction; the act that spends it is argon_app's, below.
  require_env ARGON_AUTH_PASSWORD
  pg_as argon_auth "$db" -q -c "INSERT INTO auth_proofs (id, user_id, method, act_kind) VALUES ($PROOF, $ACTOR, 'pin', 'ledger_batch');" </dev/null >/dev/null
  BATCH_SQL="INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($ACT, 'ledger_batch', $ACTOR, $PROOF);
INSERT INTO ledger_batches (id, society_id, reference, actor_user_id, row_count, total_kobo, authorised_by)
  VALUES ($BT, $S, 'sha256:probe', $ACTOR, 2, 1010000, $ACT);"
else
  BATCH_SQL="INSERT INTO ledger_batches (id, society_id, reference, actor_user_id, row_count, total_kobo)
  VALUES ($BT, $S, 'sha256:probe', $ACTOR, 2, 1010000);"
fi
# A signature rests on the signer's own step-up act (0010), where the schema has
# them: argon_auth mints each proof in its own transaction; the act that spends
# it is argon_app's, in the same statement as the signature. Before 0010 a
# signature names no act.
has_sig_acts=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM information_schema.columns
  WHERE table_name = 'signatory_approvals' AND column_name = 'act_id'" </dev/null)
sign_sql() {  # sign_sql PROOF ACT USER MEMBER POST: one signature on R1
  sign_on_sql "$R1" "$@"
}
sign_on_sql() {  # sign_on_sql REQUEST PROOF ACT USER MEMBER POST
  if [[ $has_sig_acts == 1 ]]; then
    echo "INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($3, 'approval_signature', $4, $2);
INSERT INTO signatory_approvals (request_id, member_id, post_at_signing, act_id) VALUES ($1, $5, '$6', $3);"
  else
    echo "INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($1, $5, '$6');"
  fi
}
# Single contributions and withdrawals (0011), where the schema has them. The
# app may not set a society's quorum, floor or charges (grants.sql): the
# operator does, here as the migrator, between the app's society and the rest.
has_money=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM information_schema.columns
  WHERE table_name = 'societies' AND column_name = 'account_floor_kobo'" </dev/null)
# The payout hold (0012): a payout is authorised (held) before money moves,
# and whatever happened is recorded, any difference flagged.
has_holds=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM information_schema.columns
  WHERE table_name = 'ledger_entries' AND column_name = 'hold_id'" </dev/null)
# 0015: a single contribution names the client's reference; nobody authorises
# or attests to a movement they benefit from; the authoriser never records.
# From the catalog (finding 34), not a privilege-filtered view.
has_benefit=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM pg_attribute WHERE attrelid = 'public.ledger_entries'::regclass
  AND attname = 'client_reference' AND NOT attisdropped" </dev/null)
# cref NAME: the column and value a single contribution carries from 0015.
CR_COL="" ; [[ $has_benefit == 1 ]] && CR_COL=", client_reference"
# Never `[[ … ]] && printf` here: false on an older schema, the function
# returns 1, and an assignment from it exits the probe silently (finding 7).
cref() { if [[ $has_benefit == 1 ]]; then printf ", 'PROBE-%s'" "$1"; fi; }
# 0016: a withdrawal request names its raiser and rests on the raiser's own
# step-up act, written in its transaction; it is within withdrawable when
# raised. From the catalog (finding 34).
has_raisers=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM pg_attribute WHERE attrelid = 'public.approval_requests'::regclass
  AND attname = 'raised_by_user_id' AND NOT attisdropped" </dev/null)
# 0017: a request keeps the charge it was quoted; the largest amount a member
# may request. From the catalog (finding 34).
has_quotes=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM pg_attribute WHERE attrelid = 'public.approval_requests'::regclass
  AND attname = 'quoted_on' AND NOT attisdropped" </dev/null)
# raised_sql REQUEST REFERENCE AMOUNT PROOF ACT: M2's own withdrawal request.
# From 0016 on M2's own withdrawal_request act, in the same transaction; before
# it a request names no raiser. (if/fi, never `[[ ]] &&`: finding 7.)
raised_sql() {
  if [[ $has_raisers == 1 ]]; then
    echo "INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($5, 'withdrawal_request', $U2, $4);
INSERT INTO approval_requests (id, society_id, reference, member_id, kind, amount_kobo, required, expires_at, raised_by_user_id, act_id)
  VALUES ($1, $S, '$2', $M2, 'withdrawal_out', $3, 2, now() + interval '1 day', $U2, $5);"
  else
    echo "INSERT INTO approval_requests (id, society_id, reference, member_id, kind, amount_kobo, required, expires_at)
  VALUES ($1, $S, '$2', $M2, 'withdrawal_out', $3, 2, now() + interval '1 day');"
  fi
}
if [[ $has_raisers == 1 ]]; then
  # The proofs 0016's raises spend: R1-R3, K45 and P45 are M2's own (U2);
  # P46's act is the treasurer's (U1) for M2's request; P47 is the treasurer
  # raising for M2.
  pg_as argon_auth "$db" -q -c "INSERT INTO auth_proofs (id, user_id, method, act_kind) VALUES
    ($SPW1, $U2, 'pin', 'withdrawal_request'), ($SPW2, $U2, 'pin', 'withdrawal_request'),
    ($SPW3, $U2, 'pin', 'withdrawal_request'), ($SPW4, $U2, 'pin', 'withdrawal_request'),
    ($SPW5, $U1, 'pin', 'withdrawal_request'), ($SPW6, $U1, 'pin', 'withdrawal_request'),
    ($SPW7, $U2, 'pin', 'withdrawal_request'), ($SPW8, $U2, 'pin', 'withdrawal_request');" </dev/null >/dev/null
fi
# VAT's era (0013): before it, VAT was on the partner's fee and the probe's ₦10
# sat there; from it, VAT is on Argon's own fee, the ₦10 is Argon's, and the
# partner's is zero AS PROBE DATA ONLY (unknown in reality, and refused).
has_vat_era=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM pg_constraint WHERE conname = 'charge_schedules_component_check'
  AND pg_get_constraintdef(oid) LIKE '%vat_on_argon_fee%'" </dev/null)
if [[ $has_vat_era == 1 ]]; then VAT_COMP=vat_on_argon_fee VAT_ROLE=vat FEE_COMP=argon_margin FEE_ROLE=argon_margin
else VAT_COMP=partner_fee_vat VAT_ROLE=partner_vat FEE_COMP=partner_fee FEE_ROLE=partner_charge; fi
A5=$(U 25)
H1=$(U 65) E5=$(U 48) SP13=$(U 143) SP14=$(U 144) SA13=$(U 153) SA14=$(U 154)
E6=$(U 49) E7=$(U 50) E8=$(U 51) E9=$(U 52) SP15=$(U 145) SP16=$(U 146) SP17=$(U 147) SP18=$(U 148) SA15=$(U 155) SA16=$(U 156) SA17=$(U 157) SA18=$(U 158)
if [[ $has_sig_acts == 1 ]]; then
  require_env ARGON_AUTH_PASSWORD
  pg_as argon_auth "$db" -q -c "INSERT INTO auth_proofs (id, user_id, method, act_kind) VALUES
    ($SP1, $U1, 'pin', 'approval_signature'), ($SP2, $U1, 'pin', 'approval_signature'),
    ($SP3, $U1, 'pin', 'approval_signature'), ($SP4, $U3, 'pin', 'approval_signature'),
    ($SP5, $U1, 'pin', 'approval_rejection');" </dev/null >/dev/null
fi
pg_as argon_app "$db" -q -c "INSERT INTO societies (id, code, registered_name, registration_number, state_directorate)
  VALUES ($S, 'ZZPROBE', 'Probe Cooperative', 'PROBE/0001', 'Lagos');" </dev/null >/dev/null
MONEY_ACCOUNTS="" MONEY_REQUEST=""
if [[ $has_money == 1 ]]; then
  # PROBE TEST DATA, not regulation: a ₦10 fee (the partner's before 0013,
  # Argon's from it; see has_vat_era), VAT 7.5% of it, no levy, no
  # margin, a ₦0 floor, two signatures.
  pg_as argon_migrator "$db" -q <<SQL >/dev/null
BEGIN;
UPDATE societies SET withdrawal_signatures_required = 2, account_floor_kobo = 0 WHERE id = $S;
WITH s AS (
  INSERT INTO charge_schedules (society_id, kind, component, effective_from, source)
  SELECT $S, 'withdrawal_out', c, '2020-01-01', 'PROBE TEST DATA'
    FROM unnest(ARRAY['partner_fee', '$VAT_COMP', 'statutory_levy', 'argon_margin']) c
  RETURNING id, component)
INSERT INTO charge_bands (schedule_id, from_kobo, flat_kobo, bp)
  SELECT id, 0, CASE component WHEN '$FEE_COMP' THEN 1000 ELSE 0 END, CASE component WHEN '$VAT_COMP' THEN 750 ELSE 0 END FROM s;
COMMIT;
SQL
  pg_as argon_auth "$db" -q -c "INSERT INTO auth_proofs (id, user_id, method, act_kind) VALUES
    ($SP6, $U1, 'pin', 'contribution_record'), ($SP7, $U1, 'pin', 'approval_signature'),
    ($SP8, $U3, 'pin', 'approval_signature'), ($SP9, $U1, 'pin', 'withdrawal_payout'),
    ($SP10, $U1, 'pin', 'withdrawal_payout');" </dev/null >/dev/null
  if [[ $has_holds == 1 ]]; then
    pg_as argon_auth "$db" -q -c "INSERT INTO auth_proofs (id, user_id, method, act_kind) VALUES
      ($SP13, $U3, 'pin', 'withdrawal_authorisation'), ($SP14, $U1, 'pin', 'withdrawal_payout');" </dev/null >/dev/null
  # 0015: the proofs its cases spend.
  [[ $has_benefit == 1 ]] && pg_as argon_auth "$db" -q -c "INSERT INTO auth_proofs (id, user_id, method, act_kind) VALUES
      ($SP15, $U1, 'pin', 'contribution_record'), ($SP16, $U3, 'pin', 'withdrawal_payout'),
      ($SP17, $U1, 'pin', 'contribution_record'), ($SP18, $U1, 'pin', 'contribution_record');" </dev/null >/dev/null
  fi
  MONEY_ACCOUNTS=",
  ($A3, $S, '2101', 'Charges payable — partner', 'liability', 'charges_partner'),
  ($A4, $S, '2102', 'VAT payable on charges', 'liability', 'charges_vat'),
  ($A5, $S, '2111', 'Charges payable — Argon', 'liability', 'charges_margin')"
  [[ $FEE_ROLE == argon_margin ]] && FEE_ACCT=$A5 || FEE_ACCT=$A3
  # R3: ₦5,000 out of M2's savings, approved by M1 and M3 (neither is M2).
  MONEY_REQUEST="$(raised_sql "$R3" APR-PROBE-3 500000 "$SPW3" "$SAW3")
$(sign_on_sql "$R3" "$SP7" "$SA7" "$U1" "$M1" treasurer)
$(sign_on_sql "$R3" "$SP8" "$SA8" "$U3" "$M3" chairman)"
fi
pg_as argon_app "$db" -q <<SQL >/dev/null
BEGIN;
INSERT INTO branches (id, society_id, code, name) VALUES ($B, $S, 'HQ', 'Head office');
INSERT INTO members (id, society_id, branch_id, member_number, full_name, phone, collection_channel, joined_on, user_id) VALUES
  ($M1, $S, $B, 'P-001', 'Probe Treasurer', '+2340000000001', 'payroll', '2020-01-01', $U1),
  ($M2, $S, $B, 'P-002', 'Probe Auditor',   '+2340000000002', 'payroll', '2020-01-01', $U2),
  ($M3, $S, $B, 'P-003', 'Probe Chairman',  '+2340000000003', 'cash',    '2020-01-01', $U3);
INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from) VALUES
  ($S, $M1, 'treasurer', true, '2020-01-01'),
  ($S, $M3, 'chairman',  true, '2020-01-01');
INSERT INTO accounts (id, society_id, code, name, kind, role) VALUES
  ($A1, $S, '1000', 'Cash at VFD', 'asset', 'bank'),
  ($A2, $S, '2000', 'Member savings', 'liability', 'member_savings')$MONEY_ACCOUNTS;
$BATCH_SQL
INSERT INTO ledger_entries (id, society_id, batch_id, kind, value_date, actor_user_id, actor_post, actor_post_label)
  VALUES ($E1, $S, $BT, 'contribution_in', '2026-01-31', $ACTOR, 'treasurer', 'Treasurer');
INSERT INTO ledger_lines (id, entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
  ($L1, $E1, $A1,  10000, 'principal', NULL),
  ($L2, $E1, $A2, -10000, 'principal', $M1);
-- M2's ₦10,000, imported in the same batch, BEFORE anything is asked of them:
-- from 0016 a request is within withdrawable when raised, and a request for
-- money a member does not have describes a world the society cannot be in
-- (finding 44). R1 and R2 asked ₦500,000 each against nothing until 2026-10-20.
INSERT INTO ledger_entries (id, society_id, batch_id, kind, value_date, actor_user_id, actor_post, actor_post_label)
  VALUES ($E0, $S, $BT, 'contribution_in', '2026-01-31', $ACTOR, 'treasurer', 'Treasurer');
INSERT INTO ledger_lines (id, entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
  ($L3, $E0, $A1,  1000000, 'principal', NULL),
  ($L4, $E0, $A2, -1000000, 'principal', $M2);
$(raised_sql "$R1" APR-PROBE-1 100000 "$SPW1" "$SAW1")
$(raised_sql "$R2" APR-PROBE-2 100000 "$SPW2" "$SAW2")
$(sign_sql "$SP1" "$SA1" "$U1" "$M1" treasurer)
$MONEY_REQUEST
COMMIT;
SQL
n=$(pg_as argon_app "$db" -At -c "SELECT count(*) FROM ledger_lines WHERE entry_id = $E1" </dev/null)
[[ $n == 2 ]] || die "fixtures not in place (ledger_lines for E1 = '$n') — every result below would be meaningless"
ok "fixtures committed: a batch of 2 posted entries (M1's and M2's savings), M2's approval requests, R1's first signature"

# ---- harness -----------------------------------------------------------------
declare -a ROWS; req_fail=0; gaps=0
run_sql() {  # role sql -> sets OUT, RC
  local role=$1 sql=$2
  RC=0
  OUT=$(printf '\\set VERBOSITY verbose\n%s\n' "$sql" | pg_as "$role" "$db" -q 2>&1) || RC=$?
}
first_error() { grep -m1 -E '^(psql:.*)?ERROR' <<<"$OUT" | sed -E 's/^psql:[^E]*//' | cut -c1-110; }

# probe CLASS ID ROLE EXPECT_ERE DESCRIPTION SQL [ALSO_REQUIRE_ERE]
#   CLASS: req (must be refused) | gap (should be refused; reported) | ctl (must SUCCEED)
probe() {
  local class=$1 id=$2 role=$3 expect=$4 desc=$5 sql=$6 also=${7:-}
  run_sql "$role" "$sql"
  local verdict detail
  if [[ $class == ctl ]]; then
    if ((RC == 0)); then verdict="OK        ✓"; detail="control committed"
    else verdict="CTL FAIL  ?"; detail="control should succeed: $(first_error)"; req_fail=1; fi
  # Matched against the whole report on one line: psql prints a RAISE's
  # constraint name on its own "CONSTRAINT NAME:" line, apart from the SQLSTATE.
  elif ((RC != 0)) && grep -qE "$expect" <<<"$(tr '\n' ' ' <<<"$OUT")" && { [[ -z $also ]] || grep -qE "$also" <<<"$OUT"; }; then
    verdict="REFUSED   ✓"; detail=$(first_error)
  elif ((RC != 0)); then
    verdict="WRONG ERR ?"; detail="$(first_error)"; [[ $class == req ]] && req_fail=1
  else
    if [[ $class == req ]]; then verdict="SUCCEEDED ✗ DECORATIVE"; req_fail=1
    else verdict="ACCEPTED  ✗ GAP"; gaps=$((gaps+1)); fi
    detail="statement committed without error"
  fi
  ROWS+=("$(printf '%-4s %-3s %-14s %-23s %s\n                                              → %s' "$id" "$class" "$role" "$verdict" "$desc" "$detail")")
  printf '%-4s %-24s %s\n' "$id" "$verdict" "$desc" >&2
}

OWNER=argon_migrator   # lands in argon_owner on connect

info "probing $db"

# == Your five cases ===========================================================
# 1–2. Immutability at the TRIGGER layer. Run as the owner, who has UPDATE and
# DELETE privileges, so nothing but the trigger stands in the way.
probe req T1 $OWNER 'P0001.*immutable: UPDATE on ledger_entries' "UPDATE a posted ledger_entries row" \
  "UPDATE ledger_entries SET reason = 'probe: rewriting a posted entry' WHERE id = $E1;"
probe req T2 $OWNER 'P0001.*immutable: UPDATE on ledger_lines'   "UPDATE a posted ledger_lines amount" \
  "UPDATE ledger_lines SET amount_kobo = 99999 WHERE id = $L1;"
probe req T3 $OWNER 'P0001.*immutable: UPDATE on ledger_batches' "UPDATE a posted ledger_batches total" \
  "UPDATE ledger_batches SET total_kobo = 0 WHERE id = $BT;"
probe req T4 $OWNER 'P0001.*immutable: DELETE on ledger_entries' "DELETE a posted ledger_entries row" \
  "DELETE FROM ledger_entries WHERE id = $E1;"
probe req T5 $OWNER 'P0001.*immutable: DELETE on ledger_lines'   "DELETE a posted ledger_lines row" \
  "DELETE FROM ledger_lines WHERE id = $L1;"
probe req T6 $OWNER 'P0001.*immutable: DELETE on ledger_batches' "DELETE a posted ledger_batches row" \
  "DELETE FROM ledger_batches WHERE id = $BT;"

# 3. Balance, enforced at COMMIT. The marker proves both INSERTs succeeded and
# the refusal came from COMMIT — i.e. the deferred trigger, not an early check.
# A single contribution rests on its poster's act where the schema has them
# (0011), so the only refusal C3 can meet is the balance check it is about.
SP11=$(U 141) SP12=$(U 142) SA11=$(U 151) SA12=$(U 152)
C3_ACT="" C3_COL="" C3_VAL="" K3_ACT="" K3_COL="" K3_VAL=""
if [[ $has_money == 1 ]]; then
  pg_as argon_auth "$db" -q -c "INSERT INTO auth_proofs (id, user_id, method, act_kind) VALUES
    ($SP11, $ACTOR, 'pin', 'contribution_record'), ($SP12, $ACTOR, 'pin', 'contribution_record');" </dev/null >/dev/null
  C3_ACT="INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA11, 'contribution_record', $ACTOR, $SP11);" C3_COL=", act_id$CR_COL" C3_VAL=", $SA11$(cref C3)"
  K3_ACT="INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA12, 'contribution_record', $ACTOR, $SP12);" K3_COL=", act_id$CR_COL" K3_VAL=", $SA12$(cref K3)"
fi
probe req C3 argon_app "P0001.*entry a0000000-0000-4000-8000-000000000042 does not balance: debits minus credits = 100 kobo" \
  "entry whose lines sum to +100 kobo fails at COMMIT" \
  "BEGIN; $C3_ACT
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label$C3_COL)
     VALUES ($EBAD, $S, 'contribution_in', '2026-02-01', $ACTOR, 'treasurer', 'Treasurer'$C3_VAL);
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role) VALUES ($EBAD, $A1,  10000, 'principal');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES ($EBAD, $A2,  -9900, 'principal', $M1);
   \\echo PROBE_MARKER_LINES_INSERTED_BEFORE_COMMIT
   COMMIT;" 'PROBE_MARKER_LINES_INSERTED_BEFORE_COMMIT'
probe req C3b argon_app 'P0001.*C3b-entry-persisted' "…and the unbalanced entry left nothing behind" \
  "DO \$\$ BEGIN IF (SELECT count(*) FROM ledger_entries WHERE id = $EBAD) + (SELECT count(*) FROM ledger_lines WHERE entry_id = $EBAD) > 0
     THEN RAISE NOTICE 'persisted'; ELSE RAISE EXCEPTION 'C3b-entry-persisted: none (this refusal is the pass)'; END IF; END \$\$;"
probe ctl K3 argon_app '' "control: the same entry balanced (+10000/−10000) commits" \
  "BEGIN; $K3_ACT
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label$K3_COL)
     VALUES ($EOK, $S, 'contribution_in', '2026-02-01', $ACTOR, 'treasurer', 'Treasurer'$K3_VAL);
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role) VALUES ($EOK, $A1,  10000, 'principal');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES ($EOK, $A2, -10000, 'principal', $M1);
   COMMIT;"

# 4. Internal auditor as signatory.
probe req C4 argon_app '23514.*auditor_never_signs' "appoint an internal_auditor with is_signatory = true" \
  "INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from)
     VALUES ($S, $M2, 'internal_auditor', true, '2026-01-01');"
probe ctl K4 argon_app '' "control: same appointment with is_signatory = false commits" \
  "INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from)
     VALUES ($S, $M2, 'internal_auditor', false, '2026-01-01');"

# 5. One member, one signature per request.
# Each with its own fresh step-up where the schema has them, so the refusal is
# the one-per-person rule's, not the step-up's. BEGIN/COMMIT: the probe runs a
# case statement by statement, and the act and the signature are one event.
probe req C5 argon_app '23505.*signatory_approvals_one_per_user' "same member signs the same approval request twice" \
  "BEGIN; $(sign_sql "$SP2" "$SA2" "$U1" "$M1" treasurer) COMMIT;"
probe req C5b argon_app '23505.*signatory_approvals_one_per_user' "…twice, under a different post label" \
  "BEGIN; $(sign_sql "$SP3" "$SA3" "$U1" "$M1" chairman) COMMIT;"
probe ctl K5 argon_app '' "control: a different person, the current chairman, signs the same request" \
  "BEGIN; $(sign_sql "$SP4" "$SA4" "$U3" "$M3" chairman) COMMIT;"
# 0010 as the app (findings 16 and 25): the step-up rules refuse the app's
# unauthorised signature and rejection (P23, P24), and the app can reject on
# the rejecter's own act (K23), with the grants it has. P24 runs before K23 on
# the same request: refused, it changes nothing.
if [[ $has_sig_acts == 1 ]]; then
probe req P23 argon_app '23514.*a signature rests on the signer' "app signs with no step-up behind it"   "INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($R2, $M1, 'treasurer');"
probe req P24 argon_app '23514.*a rejection rests on the rejecter' "app rejects with no step-up behind it"   "INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason) VALUES ($R2, $M1, $U1, 'treasurer', 'amount_wrong');"
probe ctl K23 argon_app '' "control: app rejects on the rejecter's own step-up, in one transaction"   "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA5, 'approval_rejection', $U1, $SP5);
   INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, act_id) VALUES ($R2, $M1, $U1, 'treasurer', 'amount_wrong', $SA5); COMMIT;"
fi

# == Privilege layer: what argon_app cannot even attempt =======================
# 0011 as the app: governance and charges are not the app's to write (P25–P30,
# by INSERT as well as UPDATE: an app that could INSERT a society's quorum
# could create one with a quorum of 1); its descriptive fields are (K26). The
# money path runs as the app it serves: a contribution with no step-up is
# refused (P31); one on the poster's own act commits (K25); an approved
# withdrawal is paid out, charges on top (K27), once (P32).
if [[ $has_money == 1 ]]; then
probe req P25 argon_app '42501.*permission denied for table charge_schedules' "app writes a charge series" \
  "INSERT INTO charge_schedules (society_id, kind, component, effective_from, source) VALUES ($S, 'withdrawal_out', 'argon_margin', '2026-01-01', 'app');"
probe req P26 argon_app '42501.*permission denied for table societies' "app sets its society's withdrawal quorum to 1" \
  "UPDATE societies SET withdrawal_signatures_required = 1 WHERE id = $S;"
probe req P27 argon_app '42501.*permission denied for table societies' "app creates a society that carries a withdrawal quorum of 1" \
  "INSERT INTO societies (code, registered_name, registration_number, state_directorate, withdrawal_signatures_required) VALUES ('ZZP27', 'Quorum of one', 'P/27', 'Lagos', 1);"
probe req P28 argon_app '42501.*permission denied for table societies' "app lowers its society's account floor" \
  "UPDATE societies SET account_floor_kobo = 0 WHERE id = $S;"
probe req P29 argon_app '42501.*permission denied for table societies' "app sets its society's write-off quorum" \
  "UPDATE societies SET write_off_signatures_required = 1 WHERE id = $S;"
probe req P30 argon_app '42501.*permission denied for table societies' "app lowers its society's general signature count" \
  "UPDATE societies SET signatures_required = 1 WHERE id = $S;"
probe ctl K26 argon_app '' "control: app corrects its society's registered name (a descriptive field)" \
  "UPDATE societies SET registered_name = 'Probe Cooperative Society' WHERE id = $S;"
probe req P31 argon_app '23514.*rests on its poster' "app records a contribution with no step-up behind it" \
  "BEGIN; INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label$CR_COL) VALUES ($E2, $S, 'contribution_in', current_date, $U1, 'treasurer', 'Treasurer'$(cref P31));
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id) VALUES ($E2, $A1, 1000000, NULL), ($E2, $A2, -1000000, $M2); COMMIT;"
probe ctl K25 argon_app '' "control: app records a ₦10,000 contribution on the poster's own step-up" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA6, 'contribution_record', $U1, $SP6);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id$CR_COL) VALUES ($E2, $S, 'contribution_in', current_date, $U1, 'treasurer', 'Treasurer', $SA6$(cref K25));
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id) VALUES ($E2, $A1, 1000000, NULL), ($E2, $A2, -1000000, $M2); COMMIT;"
if [[ $has_holds == 1 ]]; then
# 0012: authorise (hold) before the money moves; then record whatever happened.
probe req P33 argon_app '42501.*permission denied for table withdrawal_holds' "app supplies a hold's charges (the server computes them)"   "INSERT INTO withdrawal_holds (request_id, society_id, member_id, amount_kobo, partner_fee_kobo, actor_user_id) VALUES ($R3, $S, $M2, 500000, 0, $U1);"
# The CHAIRMAN authorises (U3), so the treasurer (U1) may record its payout
# below: from 0015 the officer who authorised a hold never records it.
probe ctl K29 argon_app '' "control: app authorises the approved ₦5,000 payout on the chairman's own step-up (a hold)"   "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA13, 'withdrawal_authorisation', $U3, $SP13);
   INSERT INTO withdrawal_holds (id, request_id, society_id, member_id, amount_kobo, actor_user_id, act_id) VALUES ($H1, $R3, $S, $M2, 500000, $U3, $SA13); COMMIT;"
if [[ $has_benefit == 1 ]]; then
# 0015, before the hold is paid (a paid hold is refused by hold_release_unpaid,
# which sorts first, and would cover for the rule under test).
probe req P44 argon_app '23514.*releaser_not_beneficiary' "app releases a hold as the member it pays (frees their own withdrawable)"   "INSERT INTO withdrawal_hold_releases (hold_id, actor_user_id, reason) VALUES ($H1, $U2, 'probe: released by its own beneficiary');"
fi
probe ctl K27 argon_app '' "control: app records the held ₦5,000 payout, ₦10.75 on top, on its own step-up"   "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA9, 'withdrawal_payout', $U1, $SP9);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id, request_id, hold_id, payout_reference) VALUES ($E3, $S, 'withdrawal_out', current_date, $U1, 'treasurer', 'Treasurer', $SA9, $R3, $H1, 'NIP-PROBE-1');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
     ($E3, $A2, 501075, 'principal', $M2), ($E3, $A1, -500000, 'principal', NULL), ($E3, $FEE_ACCT, -1000, '$FEE_ROLE', NULL), ($E3, $A4, -75, '$VAT_ROLE', NULL); COMMIT;"
probe req P32 argon_app '23505.*payout_recorded_once' "app records the same bank transfer a second time"   "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA10, 'withdrawal_payout', $U1, $SP10);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id, request_id, hold_id, payout_reference) VALUES ($E4, $S, 'withdrawal_out', current_date, $U1, 'treasurer', 'Treasurer', $SA10, $R3, $H1, 'NIP-PROBE-1'); COMMIT;"
probe ctl K28 argon_app '' "control: a second, different transfer on the same hold is RECORDED (and flagged), not refused"   "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA14, 'withdrawal_payout', $U1, $SP14);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id, request_id, hold_id, payout_reference) VALUES ($E5, $S, 'withdrawal_out', current_date, $U1, 'treasurer', 'Treasurer', $SA14, $R3, $H1, 'NIP-PROBE-2');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
     ($E5, $A2, 501075, 'principal', $M2), ($E5, $A1, -500000, 'principal', NULL), ($E5, $FEE_ACCT, -1000, '$FEE_ROLE', NULL), ($E5, $A4, -75, '$VAT_ROLE', NULL); COMMIT;"
probe req P34 argon_app 'P0001.*K28-unflagged' "…and the second transfer was flagged for a person (hold_paid_twice)"   "DO \$\$ BEGIN IF EXISTS (SELECT 1 FROM ledger_discrepancies WHERE entry_id = $E5 AND kind = 'hold_paid_twice' AND expected <> '' AND observed <> '')
     THEN RAISE EXCEPTION 'K28-unflagged: no, it was flagged (this refusal is the pass)'; ELSE RAISE NOTICE 'unflagged'; END IF; END \$\$;"
probe req P35 argon_app '42501.*permission denied for table ledger_discrepancies' "app writes away a flag"   "DELETE FROM ledger_discrepancies WHERE entry_id = $E5;"
else
probe ctl K27 argon_app '' "control: app pays out an approved ₦5,000 withdrawal, ₦10.75 on top, on its own step-up" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA9, 'withdrawal_payout', $U1, $SP9);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id, request_id, payout_reference) VALUES ($E3, $S, 'withdrawal_out', current_date, $U1, 'treasurer', 'Treasurer', $SA9, $R3, 'NIP-PROBE-1');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
     ($E3, $A2, 501075, 'principal', $M2), ($E3, $A1, -500000, 'principal', NULL), ($E3, $FEE_ACCT, -1000, '$FEE_ROLE', NULL), ($E3, $A4, -75, '$VAT_ROLE', NULL); COMMIT;"
probe req P32 argon_app '23505.*withdrawal_posted_once' "app pays the same approved request a second time" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA10, 'withdrawal_payout', $U1, $SP10);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id, request_id, payout_reference) VALUES ($E4, $S, 'withdrawal_out', current_date, $U1, 'treasurer', 'Treasurer', $SA10, $R3, 'NIP-PROBE-2'); COMMIT;"
fi
fi

# 0015: nobody authorises or attests to a movement they benefit from; the
# authoriser never records; a single contribution names its client reference,
# once per society. As the app, whose writes these bind. Each case is valid in
# every other respect, so only its own rule can refuse it.
if [[ $has_benefit == 1 ]]; then
probe req P40 argon_app '23514.*contribution_not_recorded_by_beneficiary' "app records money into the recorder's own savings" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA15, 'contribution_record', $U1, $SP15);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id, client_reference) VALUES ($E6, $S, 'contribution_in', current_date, $U1, 'treasurer', 'Treasurer', $SA15, 'PROBE-P40');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id) VALUES ($E6, $A1, 100000, NULL), ($E6, $A2, -100000, $M1); COMMIT;"
probe req P42 argon_app '23514.*contribution_names_client_reference' "app records a contribution with no client reference" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA17, 'contribution_record', $U1, $SP17);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id) VALUES ($E7, $S, 'contribution_in', current_date, $U1, 'treasurer', 'Treasurer', $SA17);
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id) VALUES ($E7, $A1, 100000, NULL), ($E7, $A2, -100000, $M2); COMMIT;"
probe req P43 argon_app '23505.*contribution_recorded_once' "app records a contribution under a client reference already used (a retry)" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA18, 'contribution_record', $U1, $SP18);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id, client_reference) VALUES ($E8, $S, 'contribution_in', current_date, $U1, 'treasurer', 'Treasurer', $SA18, 'PROBE-K25');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, member_id) VALUES ($E8, $A1, 100000, NULL), ($E8, $A2, -100000, $M2); COMMIT;"
probe req P41 argon_app '23514.*authoriser_never_records' "app records a payout on the hold its recorder authorised" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SA16, 'withdrawal_payout', $U3, $SP16);
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, act_id, request_id, hold_id, payout_reference) VALUES ($E9, $S, 'withdrawal_out', current_date, $U3, 'chairman', 'Chairman', $SA16, $R3, $H1, 'NIP-PROBE-P41');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
     ($E9, $A2, 501075, 'principal', $M2), ($E9, $A1, -500000, 'principal', NULL), ($E9, $FEE_ACCT, -1000, '$FEE_ROLE', NULL), ($E9, $A4, -75, '$VAT_ROLE', NULL); COMMIT;"
else
  info "schema before 0015: no benefit or client-reference rules to probe (P40–P44 not run)"
fi

# 0016: a withdrawal request has an author, on their own step-up, and a quorum
# that can approve it. As the app, whose writes these bind; each case valid in
# every other respect (₦1,000, within M2's withdrawable), so only its own rule
# can refuse it.
if [[ $has_raisers == 1 ]]; then
probe req P45 argon_app '23514.*withdrawal_request_names_raiser' "app raises a withdrawal request that names no raiser" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SAW7, 'withdrawal_request', $U2, $SPW7);
   INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at, act_id)
     VALUES ($S, 'APR-PROBE-P45', $M2, 'withdrawal_out', 100000, 2, now() + interval '1 day', $SAW7); COMMIT;"
probe req P46 argon_app '23514.*withdrawal_request_act_is_raisers' "app raises M2's request on the treasurer's step-up" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SAW5, 'withdrawal_request', $U1, $SPW5);
   INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at, raised_by_user_id, act_id)
     VALUES ($S, 'APR-PROBE-P46', $M2, 'withdrawal_out', 100000, 2, now() + interval '1 day', $U2, $SAW5); COMMIT;"
probe req P47 argon_app '23514.*request_cannot_reach_quorum' "app raises, as the treasurer, a request only the chairman could sign (quorum 2)" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SAW6, 'withdrawal_request', $U1, $SPW6);
   INSERT INTO approval_requests (society_id, reference, member_id, kind, amount_kobo, required, expires_at, raised_by_user_id, act_id)
     VALUES ($S, 'APR-PROBE-P47', $M2, 'withdrawal_out', 100000, 2, now() + interval '1 day', $U1, $SAW6); COMMIT;" \
  '1 signatories may sign it.*needs 2.*Appoint another signatory'
probe req P48 argon_app '42501.*permission denied for function operator_set_withdrawal_request_days' "app sets its society's request window through the operator's function" \
  "SELECT operator_set_withdrawal_request_days($S, 30, 'app', 'app', 'none', current_date, 'app');"
probe ctl K45 argon_app '' "control: app raises M2's own ₦1,000 request on M2's own step-up (the server sets its expiry)" \
  "BEGIN; $(raised_sql "$RK45" APR-PROBE-K45 100000 "$SPW4" "$SAW4") COMMIT;"
else
  info "schema before 0016: no withdrawal-request authorship rules to probe (P45-P48, K45 not run)"
fi

# 0017, as the app whose balance handler calls it: the largest amount a member
# may request, exactly (the probe's series is ₦10 flat at every amount and 7.5%
# VAT on it, so from ₦10,000 it is ₦9,989.25); and a request the app raises
# keeps the SERVER's quote, whatever the app sends (₦10.75 on ₦1,000, today).
if [[ $has_quotes == 1 ]]; then
probe ctl K46 argon_app '' "control: app asks the largest request from ₦10,000 and gets ₦9,989.25 exactly" \
  "DO \$\$ BEGIN IF largest_withdrawal($S, 'withdrawal_out', 1000000, current_date) IS DISTINCT FROM 998925 THEN
     RAISE EXCEPTION 'K46: largest_withdrawal(1000000) answered %, want 998925', largest_withdrawal($S, 'withdrawal_out', 1000000, current_date); END IF; END \$\$;"
probe ctl K47 argon_app '' "control: app raises M2's request sending its own quote; the request keeps the server's" \
  "BEGIN; INSERT INTO credential_acts (id, kind, actor_user_id, proof_id) VALUES ($SAW8, 'withdrawal_request', $U2, $SPW8);
   INSERT INTO approval_requests (id, society_id, reference, member_id, kind, amount_kobo, required, expires_at, raised_by_user_id, act_id,
     quoted_partner_fee_kobo, quoted_vat_kobo, quoted_statutory_levy_kobo, quoted_argon_margin_kobo, quoted_on)
     VALUES ($RK47, $S, 'APR-PROBE-K47', $M2, 'withdrawal_out', 100000, 2, now() + interval '1 day', $U2, $SAW8, 1, 1, 1, 1, '2020-01-01');
   DO \$\$ BEGIN IF (SELECT quoted_partner_fee_kobo + quoted_vat_kobo + quoted_statutory_levy_kobo + quoted_argon_margin_kobo FROM approval_requests WHERE id = $RK47) IS DISTINCT FROM 1075
        OR (SELECT quoted_on FROM approval_requests WHERE id = $RK47) IS DISTINCT FROM current_date THEN
     RAISE EXCEPTION 'K47: the request kept the client''s quote'; END IF; END \$\$; COMMIT;"
else
  info "schema before 0017: no kept quote or largest request to probe (K46, K47 not run)"
fi

# 0014: the operator's functions are the operator's. The app can call none of
# them, and cannot write the log of who changed a society's rules.
# Detected from the catalog, not information_schema: information_schema lists
# only what the asking role may touch, and the app may touch none of this, so
# asking it there reads "absent" and skips every case below without a word.
has_operator=$(pg_as argon_app "$db" -At -c "SELECT (to_regclass('public.society_settings_log') IS NOT NULL)::int" </dev/null)
[[ $has_operator == 1 ]] || info "schema before 0014: no operator functions to probe (P36–P39 not run)"
if [[ $has_operator == 1 ]]; then
probe req P36 argon_app '42501.*permission denied for function operator_set_withdrawal_quorum' "app sets its society's withdrawal quorum through the operator's function"   "SELECT operator_set_withdrawal_quorum($S, 2, 'app', 'app', 'none', current_date, 'app');"
probe req P37 argon_app '42501.*permission denied for function operator_add_charge_series' "app adds a charge series through the operator's function"   "SELECT operator_add_charge_series($S, 'withdrawal_out', 'argon_margin', current_date, 'app', '[]'::jsonb, 'app', 'app', 'app');"
probe req P38 argon_app '42501.*permission denied for function operator_set_lifecycle' "app moves its society's lifecycle through the operator's function"   "SELECT operator_set_lifecycle($S, 'active', 'app', 'app', 'app');"
probe req P39 argon_app '42501.*permission denied for table society_settings_log' "app writes into the log of who changed a society's rules"   "INSERT INTO society_settings_log (society_id, setting, new_value) VALUES ($S, 'withdrawal_signatures_required', '1');"
fi

probe req P1 argon_app '42501.*permission denied for table ledger_entries' "app UPDATE on ledger_entries" \
  "UPDATE ledger_entries SET reason = 'probe tamper' WHERE id = $E1;"
probe req P2 argon_app '42501.*permission denied for table ledger_lines' "app DELETE on ledger_lines" \
  "DELETE FROM ledger_lines WHERE id = $L1;"
probe req P3 argon_app '42501.*permission denied for table ledger_lines' "app TRUNCATE ledger_lines" \
  "TRUNCATE ledger_lines;"
probe req P4 argon_app '42501.*must be owner of table ledger_entries' "app DISABLE TRIGGER on ledger_entries" \
  "ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_immutable;"
probe req P5 argon_app '42501.*permission denied to set parameter "session_replication_role"' "app SET session_replication_role = replica" \
  "SET session_replication_role = replica;"
probe req P6 argon_app '42501.*permission denied for table wallet_reconciliations' "app rewrites a reconciliation difference" \
  "UPDATE wallet_reconciliations SET difference_kobo = 0;"
probe req P7 argon_app '42501.*permission denied for table signatory_approvals' "app edits a signature (post/time frozen at signing, §4)" \
  "UPDATE signatory_approvals SET post_at_signing = 'chairman' WHERE request_id = $R1 AND member_id = $M1;"
probe req P8 argon_app '42501.*permission denied for table appointments' "app back-dates an appointment (handover must not rewrite, §4.3)" \
  "UPDATE appointments SET held_from = '2020-01-01' WHERE member_id = $M2;"
probe ctl K8 argon_app '' "control: app closes an appointment (sets held_until)" \
  "UPDATE appointments SET held_until = '2099-12-31' WHERE member_id = $M2 AND post = 'internal_auditor';"

probe req P9 argon_app '42501.*permission denied for table ledger_entries' "app supplies posted_at (no INSERT privilege on the column)"   "INSERT INTO ledger_entries (society_id, kind, value_date, posted_at, actor_user_id, actor_post_label)
     VALUES ($S, 'contribution_in', '2026-02-01', '2099-01-01', $ACTOR, 'Treasurer');"
probe req P10 $OWNER '23514.*CONSTRAINT NAME: +ledger_entries_posted_at_is_now' "owner supplies posted_at (refused for every role)"   "INSERT INTO ledger_entries (society_id, kind, value_date, posted_at, actor_user_id, actor_post_label$CR_COL)
     VALUES ($S, 'contribution_in', '2026-02-01', '2099-01-01', $ACTOR, 'Treasurer'$(cref P10));"

# == The step-up's ROLE half (0005): the path that uses a proof cannot mint it ==
# The PIN is checked in Go, not in the database, so whoever can INSERT a PIN
# proof can skip the PIN. The API's handlers run as argon_app, which must not
# be able to mint a proof or record a sign-in attempt (a forged success would
# end a lockout run). Only argon_auth, the auth service's role, can; and
# argon_auth can neither use a proof (credential_acts) nor post (ledger_*).
# A proof that is valid in EVERY other respect (scoped, once 0005 requires it),
# so the only thing that can refuse it is the privilege. An unscoped proof
# would be refused by 0005's own rule first, and P11 would then go red for the
# wrong reason even with the grant restored (it did, under sabotage).
probe req P11 argon_app '42501.*permission denied for table auth_proofs' "app mints a valid, scoped PIN proof without a PIN" \
  "DO \$p\$ BEGIN
     IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'auth_proofs' AND column_name = 'act_kind') THEN
       EXECUTE format('INSERT INTO auth_proofs (user_id, method, act_kind) VALUES (%L, ''pin'', ''ledger_batch'')', $U1);
     ELSE
       EXECUTE format('INSERT INTO auth_proofs (user_id, method) VALUES (%L, ''pin'')', $U1);
     END IF;
   END \$p\$;"
# What the app must not READ: the split took minting away, not reading. A
# handler that can read PIN hashes is an offline attack on every PIN in the
# system. (credential_acts.new_pin_hash carries the same hashes, from PIN
# changes and resets.) The app reads only what session_valid and its own
# writes need.
probe req P15 argon_app '42501.*permission denied for table auth_credentials' "app reads the PIN hashes" \
  "SELECT pin_hash FROM auth_credentials LIMIT 1;"
probe req P16 argon_app '42501.*permission denied for table credential_acts' "app reads a PIN hash carried by an act" \
  "SELECT new_pin_hash FROM credential_acts LIMIT 1;"
probe req P17 argon_app '42501.*permission denied for table auth_codes' "app reads the sign-in code digests" \
  "SELECT code_digest FROM auth_codes LIMIT 1;"
probe ctl K15 argon_app '' "control: app checks a session and reads an act's id (what its handlers need)" \
  "SELECT session_valid(repeat('a', 64)); SELECT id, kind, actor_user_id, at FROM credential_acts LIMIT 1;"
# Finding 14's privilege half: the status is derived (0007), and the app has no
# path to move a request or rewrite a rejection.
probe req P18 argon_app '42501.*permission denied for table approval_requests' "app extends a request's expiry (a request is never updated)" \
  "UPDATE approval_requests SET expires_at = now() + interval '30 days' WHERE id = $R1;"
probe req P19 argon_app '42501.*permission denied for table approval_rejections' "app supplies a rejection's time (no INSERT privilege on the column)" \
  "INSERT INTO approval_rejections (request_id, member_id, user_id, post_at_rejection, reason, rejected_at) VALUES ($R1, $M3, $U1, 'chairman', 'amount_wrong', '2026-01-01');"
probe req P20 argon_app '42501.*permission denied for table approval_rejections' "app deletes a rejection to reopen a request" \
  "DELETE FROM approval_rejections;"
probe ctl K18 argon_app '' "control: app reads a request's derived status" \
  "SELECT approval_status($R1);"
# Loans (0008). Finding 16's question asked up front: what the app still needs
# to do, it can (K19), and what it must never do, it cannot (P21, P22).
probe ctl K19 argon_app '' "control: app agrees a loan on a product, with its schedule (what its handlers need)"   "BEGIN; INSERT INTO loan_products (id, society_id, code, name, max_term_months, guarantors_required, savings_multiple_pct, allows_prepayment) VALUES ($LP, $S, 'K19', 'Probe loan', 12, 0, 300, true); INSERT INTO loans (id, society_id, member_id, product_id, principal_kobo, term_months, purpose, guarantors_required, savings_multiple_pct, interest_rate_bp, allows_prepayment) VALUES ($LN, $S, $M1, $LP, 20000, 1, 'probe', 0, 300, 0, true); INSERT INTO loan_instalments (loan_id, n, due_on, amount_kobo) VALUES ($LN, 1, '2026-12-01', 20000); COMMIT;"
# P21 needs K19's loan: an UPDATE of zero rows succeeds whatever the grants.
# (If K19 fails, the probe already fails on it.)
probe req P21 argon_app '42501.*permission denied for table loans' "app raises an agreed loan's principal"   "UPDATE loans SET principal_kobo = principal_kobo * 2;"
probe req P22 argon_app '42501.*permission denied for table loans' "app back-dates a loan's agreement (no INSERT privilege on agreed_at)"   "INSERT INTO loans (society_id, member_id, product_id, principal_kobo, term_months, purpose, guarantors_required, savings_multiple_pct, interest_rate_bp, allows_prepayment, agreed_at) VALUES ($S, $M1, $LP, 100, 1, 'x', 0, 300, 0, true, '2020-01-01');"
probe req P12 argon_app '42501.*permission denied for table sign_in_attempts' "app records a sign-in success (would end a lockout run)" \
  "INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($U1, repeat('a', 64), true);"
if [[ -n ${ARGON_AUTH_PASSWORD:-} ]]; then
  probe req P13 argon_auth '42501.*permission denied for table credential_acts' "the auth role uses a proof (writes an attributable act)" \
    "INSERT INTO credential_acts (kind, actor_user_id) VALUES ('ledger_batch', $U1);"
  probe req P14 argon_auth '42501.*permission denied for table ledger_batches' "the auth role posts a ledger batch" \
    "INSERT INTO ledger_batches (society_id, reference, actor_user_id, row_count, total_kobo) VALUES ($S, 'P14', $U1, 1, 1);"
  # Scoped once 0005 has added act_kind (which it then requires: an unscoped
  # PIN proof is refused); plain before it.
  probe ctl K11 argon_auth '' "control: the auth role mints a PIN proof (rolled back)" \
    "BEGIN;
     DO \$k\$ BEGIN
       IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'auth_proofs' AND column_name = 'act_kind') THEN
         EXECUTE format('INSERT INTO auth_proofs (user_id, method, act_kind) VALUES (%L, ''pin'', ''ledger_batch'')', $U1);
       ELSE
         EXECUTE format('INSERT INTO auth_proofs (user_id, method) VALUES (%L, ''pin'')', $U1);
       END IF;
     END \$k\$;
     ROLLBACK;"
  probe ctl K12 argon_auth '' "control: the auth role records a sign-in attempt (rolled back)" \
    "BEGIN; INSERT INTO sign_in_attempts (user_id, credential_digest, succeeded) VALUES ($U1, repeat('a', 64), false); ROLLBACK;"
else
  # Not provisioned: the separation does not exist, which is a failure, not a skip.
  for c in "P13|the auth role uses a proof" "P14|the auth role posts a ledger batch" "K11|control: the auth role mints a proof" "K12|control: the auth role records an attempt"; do
    class=req; [[ ${c:0:1} == K ]] && class=ctl
    ROWS+=("$(printf '%-4s %-3s %-14s %-23s %s\n                                              → %s' "${c%%|*}" "$class" argon_auth "NOT PROVISIONED ✗" "${c#*|}" "ARGON_AUTH_PASSWORD is unset: there is no argon_auth role, so nothing separates minting from use")")
    printf '%-4s %-24s %s\n' "${c%%|*}" "NOT PROVISIONED ✗" "${c#*|}" >&2
  done
  req_fail=1
fi

# == Closed by 0002 — were gaps under 0001 =====================================
probe req G1 argon_app '23514.*CONSTRAINT NAME: +ledger_lines_entry_open' "add a balanced pair of new lines to an already-POSTED entry" \
  "BEGIN;
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role) VALUES ($E1, $A1,  500, 'principal');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES ($E1, $A2, -500, 'principal', $M1);
   COMMIT;"
probe req G3 argon_app '23514.*CONSTRAINT NAME: +signatory_not_auditor' "the current internal auditor signs" \
  "INSERT INTO signatory_approvals (request_id, member_id, post_at_signing) VALUES ($R1, $M2, 'internal_auditor');"
probe req G4 argon_app '23514.*CONSTRAINT NAME: +auditor_holds_no_signing_post' "the current chairman (a signatory) is also appointed internal auditor" \
  "BEGIN;
   INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from, held_until)
     VALUES ($S, $M3, 'internal_auditor', false, '2025-01-01', '2026-12-31');
   INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from)
     VALUES ($S, $M3, 'treasurer', true, '2026-01-01');
   COMMIT;"
# == Gaps: edges 0002 does not cover (accepted — see deploy/README.md) =========
probe gap G2 argon_app 'ERROR' "commit a ledger entry with ZERO lines" \
  "INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post_label)
     VALUES ($EZERO, $S, 'loan_disbursement', '2026-02-01', $ACTOR, 'Treasurer');"
probe gap G7 $OWNER 'ERROR' "owner edits a signature — signatory_approvals has no immutability trigger (rolled back)" \
  "BEGIN; UPDATE signatory_approvals SET post_at_signing = 'chairman', signed_at = now() - interval '1 year'
     WHERE request_id = $R1 AND member_id = $M1; ROLLBACK;"
probe gap G8 argon_app 'ERROR' "two treasurers over overlapping historical ranges (point-in-time attribution ambiguous)" \
  "INSERT INTO appointments (society_id, member_id, post, is_signatory, held_from, held_until) VALUES
     ($S, $M1, 'treasurer', true, '2025-01-01', '2025-12-31'),
     ($S, $M2, 'treasurer', true, '2025-06-01', '2025-09-30');"
probe gap G9 argon_app 'ERROR' "reverse the same posted entry twice" \
  "BEGIN;
   INSERT INTO ledger_entries (id, society_id, kind, value_date, actor_user_id, actor_post, actor_post_label, reverses_id, reason) VALUES
     ('a0000000-0000-4000-8000-000000000045', $S, 'contribution_in', '2026-02-02', $ACTOR, 'treasurer', 'Treasurer', $E1, 'probe: posted twice in error'),
     ('a0000000-0000-4000-8000-000000000046', $S, 'contribution_in', '2026-02-02', $ACTOR, 'treasurer', 'Treasurer', $E1, 'probe: posted twice in error');
   INSERT INTO ledger_lines (entry_id, account_id, amount_kobo, charge_role, member_id) VALUES
     ('a0000000-0000-4000-8000-000000000045', $A2,  10000, 'principal', $M1), ('a0000000-0000-4000-8000-000000000045', $A1, -10000, 'principal', NULL),
     ('a0000000-0000-4000-8000-000000000046', $A2,  10000, 'principal', $M1), ('a0000000-0000-4000-8000-000000000046', $A1, -10000, 'principal', NULL);
   COMMIT;"
probe gap G5 $OWNER 'ERROR' "owner TRUNCATEs ledger_lines (rolled back)" \
  "BEGIN; TRUNCATE ledger_lines; ROLLBACK;"
probe gap G6 $OWNER 'ERROR' "owner disables the immutability trigger and rewrites an entry (rolled back)" \
  "BEGIN;
   ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_immutable;
   UPDATE ledger_entries SET reason = 'probe: history rewritten by owner' WHERE id = $E1;
   ROLLBACK;"

# ---- report ------------------------------------------------------------------
echo
echo "TIER 1 PROBE — database $db — $(date -u +%FT%TZ)"
echo "req = must be refused · ctl = control, must succeed · gap = edge case, reported"
echo "-------------------------------------------------------------------------------"
for r in "${ROWS[@]}"; do echo "$r"; done
echo "-------------------------------------------------------------------------------"
if ((req_fail)); then
  echo "RESULT: FAIL — at least one required constraint did not hold as specified."
  exit 1
fi
echo "RESULT: PASS — every required case refused with its specific error; controls succeeded."
((gaps)) && echo "        $gaps gap case(s) ACCEPTED — see G* rows."
exit 0
