# Suite 0023 — a society is founded by the operator, on its founding
# resolution, on the standard chart (decided 2026-11-06). M0 is the founding
# function with no rules: it inserts the society and nothing else.
SUITE_INCLUDE='^TestFound'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestFound'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,test/tier1/0023_interface.sql'
SUITE_MUTDIR='mutants_0023'
SUITE_GREEN_NEEDS='migrations/0023_society_founding.up.sql'   # written after the red half
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestFound0[1-9]|TestFound1[0-5]'
  [m_operator_optional]='TestFound01_AFoundingNamesItsOperator'
  [m_login_optional]='TestFound02_AFoundingRecordsTheServerLogin'
  [m_resolution_optional]='TestFound03_AFoundingCarriesTheFoundingResolution'
  [m_resolution_future_ok]='TestFound04_TheResolutionIsNotDatedInTheFuture'
  [m_reason_optional]='TestFound05_AFoundingSaysWhy'
  [m_registration_blank_ok]='TestFound06_ASocietyIsRegistered'
  [m_withdrawal_one_ok]='TestFound07_AWithdrawalQuorumBelowTwoIsRefused'
  [m_write_off_one_ok]='TestFound08_AWriteOffQuorumBelowTwoIsRefused'
  [m_signature_one_ok]='TestFound09_ASignatureCountBelowTwoIsRefused'
  [m_quorums_left_default]='TestFound10_EachQuorumIsTheResolutionsNeverADefault'
  [m_quorums_unlogged]='TestFound11_EachQuorumIsLoggedWithTheResolution'
  [m_quorum_logged_without_resolution]='TestFound11_EachQuorumIsLoggedWithTheResolution'
  [m_founding_unlogged]='TestFound12_TheFoundingIsLogged'
  [m_chart_missing_a_role]='TestFound13_ASocietyStartsWithOneAccountPerPostingRole|TestFound14_EverySocietyGetsTheSameStandardChart'
  [m_chart_own_names]='TestFound14_EverySocietyGetsTheSameStandardChart'
  [m_no_head_office]='TestFound15_ASocietyStartsAsAProspectWithAHeadOffice'
  [m_founded_active]='TestFound15_ASocietyStartsAsAProspectWithAHeadOffice'
  [c_quorum_min_three]='TestFound_Control_AFoundingOnItsResolution'
  [c_registration_needs_code_in_name]='TestFound_Control_AFoundingOnItsResolution'
)
