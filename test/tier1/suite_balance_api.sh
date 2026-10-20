# Suite balance_api — a member's withdrawable figure, what was deducted and
# why, and the withdrawal quote (indicative; the hold's charge applies).
# Sourced by mutate.sh. Run with --svc.
#
# Base: the real 0001–0016. The figure is a read the API composes; the
# enforcement is the database's (0012, 0016), and test 07 holds them together.
SUITE_INCLUDE='^TestApiBalance'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiBalance'
SVC_RUN='^TestApiBalance'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/balance.go'          # the real handlers, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [bal_floor_ignored]='TestApiBalance01_TheFigureAndTheFloor'
  [bal_guarantee_unexplained]='TestApiBalance02_AGuaranteeIsHeldInFullAndSaysWhy'
  [bal_guarantee_ignored]='TestApiBalance02_AGuaranteeIsHeldInFullAndSaysWhy|TestApiBalance06_NeverBelowZero'
  [bal_repaid_listed]='TestApiBalance03_ARepaidLoanHoldsNothing'
  [bal_floor_unset_zero]='TestApiBalance04_AnUnsetFloorShowsSavingsAndWhyNotTheRest'
  [bal_floor_unset_hides_savings]='TestApiBalance04_AnUnsetFloorShowsSavingsAndWhyNotTheRest'
  [bal_holds_ignored]='TestApiBalance05_AnAuthorisedPayoutIsSpokenFor'
  [bal_negative]='TestApiBalance06_NeverBelowZero'
  [bal_quote_ignores_charges]='TestApiBalance07_TheFigureShownIsTheFigureEnforced'
  [bal_anyone_reads]='TestApiBalance08_WhoReads'
  [bal_officer_refused]='TestApiBalance08_WhoReads'
  [bal_quote_binding]='TestApiBalance09_AQuoteSaysItIsIndicative'
  [bal_quote_stale]='TestApiBalance10_TheHoldsChargeAppliesAndTheMemberIsShownIt'
  [bal_final_charge_hidden]='TestApiBalance10_TheHoldsChargeAppliesAndTheMemberIsShownIt'
  [bal_charge_shown_early]='TestApiBalance10_TheHoldsChargeAppliesAndTheMemberIsShownIt'
  [money_as_numbers]='TestApiBalance11_MoneyIsAStringOnTheWire'
)
