# Suite 0037 — defaults nobody chose (2026-11-24, finding 75). M0 is the
# migration with every rule off (0037_interface.sql, generated with the
# mutants: test/tier1/gen0037.py).
SUITE_INCLUDE='^TestDefaults'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestDefaults'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,migrations/0035_demo_societies.up.sql,migrations/0036_founding_officers_active.up.sql,test/tier1/0037_interface.sql'
SUITE_MUTDIR='mutants_0037'
SUITE_GREEN_NEEDS='migrations/0037_defaults_said.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestDefaults0[1-5]_'
  [m_founding_ignores]='TestDefaults01_FoundingSaysTheRequestWindowAndTheGuardThreshold'
  [m_days_unchecked]='TestDefaults02_FoundingRefusesARequestWindowOutOfBounds'
  [m_status_defaulted]='TestDefaults03_AMemberWithoutAStatusIsRefused'
  [m_rate_defaulted]='TestDefaults04_ALoanProductWithoutAnInterestRateIsRefused'
  [m_postable_kept]='TestDefaults05_PostableAndTheWalletsTablesAreGone'
  [m_wallets_kept]='TestDefaults05_PostableAndTheWalletsTablesAreGone'
  [c_days_refused]='TestDefaults_Control_AFoundingThatSaysEverythingIsAccepted'
)
