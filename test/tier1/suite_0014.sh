# Suite 0014 — the operator sets a society's rules, and says who, on whose
# decision (decided 2026-10-13). Sourced by mutate.sh.
SUITE_INCLUDE='^TestOperator'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestOperator'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,test/tier1/0014_interface.sql'
SUITE_MUTDIR='mutants_0014'
SUITE_GREEN_NEEDS='migrations/0014_operator_settings.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestOperator[0-9]'
  [m_operator_optional]='TestOperator01_AChangeNamesItsOperator|TestOperator13_ASeriesNamesItsOperator'
  [m_login_optional]='TestOperator02_AChangeRecordsTheServerLogin'
  [m_resolution_optional]='TestOperator03_AQuorumChangeCarriesTheSocietysResolution|TestOperator04_AFloorChangeCarriesTheSocietysResolution'
  [m_resolution_future_ok]='TestOperator05_AResolutionIsNotDatedInTheFuture'
  [m_reason_optional]='TestOperator06_AChangeSaysWhy'
  [m_quorum_one_ok]='TestOperator07_AWithdrawalQuorumBelowTwoIsRefused'
  [m_quorum_refusal_unexplained]='TestOperator07_AWithdrawalQuorumBelowTwoIsRefused'
  [m_floor_negative_ok]='TestOperator08_AFloorIsNeverNegative'
  [m_quorum_unlogged]='TestOperator09_AQuorumChangeIsLoggedWithOldAndNew'
  [m_floor_unlogged]='TestOperator10_AFloorChangeIsLogged'
  [m_log_editable]='TestOperator11_TheLogIsNeverEdited'
  [m_backdating_ok]='TestOperator12_ASeriesIsNeverBackdated'
  [m_series_unlogged]='TestOperator14_ASeriesIsLoggedWithItsSource'
  [c_quorum_min_three]='TestOperator_Control_AQuorumOfTwoOnTheSocietysResolution'
  [c_series_future_only]='TestOperator_Control_ASeriesFromTodayOrLater'
)
