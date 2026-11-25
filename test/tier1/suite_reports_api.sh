# Suite reports_api — the reports (stage 2, 2026-11-25): the income statement,
# the cash flow by the direct method, the period report (contributions by
# branch, loans outstanding, charges split). The breaks are in the reference
# (apifake_reports_test.go). Run with --svc.
SUITE_INCLUDE='^TestApiReports'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiReports'
SVC_RUN='^TestApiReports'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,migrations/0035_demo_societies.up.sql,migrations/0036_founding_officers_active.up.sql,migrations/0037_defaults_said.up.sql,migrations/0038_member_status.up.sql,migrations/0039_vfd_as_proven.up.sql,migrations/0040_member_bank_accounts.up.sql,migrations/0041_coop_reference.up.sql,migrations/0042_disputes.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/reports.go'
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [rep_period_ignored]='TestApiReports01_TheIncomeStatementIsThePeriodsByValueDate'
  [rep_zero_rows_dropped]='TestApiReports01_TheIncomeStatementIsThePeriodsByValueDate'
  [rep_income_negative]='TestApiReports01_TheIncomeStatementIsThePeriodsByValueDate'
  [rep_cash_opening_inclusive]='TestApiReports02_TheCashFlowIsMoneyInAndOutByKind'
  [rep_cash_out_as_in]='TestApiReports02_TheCashFlowIsMoneyInAndOutByKind'
  [rep_loans_at_from]='TestApiReports03_LoansOutstandingAreAtThePeriodsEnd'
  [rep_charges_swapped]='TestApiReports04_ContributionsByBranchAndChargesSplitAsFixed'
  [rep_any_reader]='TestApiReports05_OnlyTheBooksReadersReadThem'
)
