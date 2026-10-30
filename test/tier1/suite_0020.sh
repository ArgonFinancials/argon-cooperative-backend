# Suite 0020 — one account per role in a society, except 'general' (onboarding
# piece A, 2026-10-30; the money.go finding). Sourced by mutate.sh.
SUITE_INCLUDE='^TestChartRole'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestChartRole'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,test/tier1/0020_interface.sql'
SUITE_MUTDIR='mutants_0020'
SUITE_GREEN_NEEDS='migrations/0020_account_role_once.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestChartRole01'
  [m_role_once_savings_only]='TestChartRole01_ASecondAccountOfARoleIsRefused'
  [c_general_once]='TestChartRole02_Control_GeneralAccountsAreMany'
  [c_role_once_platform]='TestChartRole03_Control_EachSocietyHasItsOwn'
)
