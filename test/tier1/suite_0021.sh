# Suite 0021 — a decided request takes no more signatures (ruled 2026-10-30).
SUITE_INCLUDE='^TestDecided'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestDecided'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,test/tier1/0021_interface.sql'
SUITE_MUTDIR='mutants_0021'
SUITE_GREEN_NEEDS='migrations/0021_decided_requests.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestDecided01'
  [m_one_extra_ok]='TestDecided01_ASignatureOnAnApprovedRequestIsRefused'
  [c_refuse_deciding]='TestDecided02_Control_TheDecidingSignatureLands'
)
