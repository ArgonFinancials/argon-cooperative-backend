# Suite 0030 — is this the person's own slip, already used? A question that
# spends nothing (2026-11-10; the frontend's finding). M0 answers nothing.
SUITE_INCLUDE='^TestSpentSlip'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestSpentSlip'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,test/tier1/0030_interface.sql'
SUITE_MUTDIR='mutants_0030'
SUITE_GREEN_NEEDS='migrations/0030_slip_used.up.sql'
CONTROL_RE='^$'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestSpentSlip01_ThePersonsOwnUsedSlipIsSaidUsed'
  [m_live_too]='TestSpentSlip02_ALiveSlipIsNotSaidUsedAndIsUntouched'
  [m_anyone]='TestSpentSlip03_AnotherPersonsUsedSlipIsNotThisPersons'
  [m_counts]='TestSpentSlip04_AWrongCodeCountsNothing'
)
