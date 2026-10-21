# Suite 0017 — a withdrawal request keeps the charge it was quoted when raised
# (finding 49); the largest amount a member may request, found exactly
# (decided 2026-10-21). Sourced by mutate.sh.
SUITE_INCLUDE='^Test(Quote|Largest)'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^Test(Quote|Largest)'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,test/tier1/0017_interface.sql'
SUITE_MUTDIR='mutants_0017'
SUITE_GREEN_NEEDS='migrations/0017_quotes_and_largest.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='Test(Quote|Largest)[0-9]'
  [m_quote_not_stored]='TestQuote01_ARequestKeepsTheChargeItWasQuoted'
  [m_quote_client_kept]='TestQuote02_AClientsQuoteIsOverwritten'
  [m_quote_other_kinds_kept]='TestQuote03_AnotherKindCarriesNoQuoteEvenIfSent'
  [c_quote_every_kind]='TestQuote_Control_ALoanRequestCarriesNoQuote'
  [m_largest_approx]='TestLargest01_ExactOneKoboBelowTheLevy|TestLargest02_ExactOneKoboBelowTheFee'
  [m_largest_single_search]='TestLargest04_WhereTheChargeFallsAboveAThreshold'
  [m_largest_off_by_one]='TestLargest01_ExactOneKoboBelowTheLevy|TestLargest02_ExactOneKoboBelowTheFee|TestLargest03_WithinOneBand|TestLargest05_TheLargestIsAcceptedOneKoboMoreIsNot'
  [m_largest_strict]='TestLargest03_WithinOneBand|TestLargest05_TheLargestIsAcceptedOneKoboMoreIsNot'
)
