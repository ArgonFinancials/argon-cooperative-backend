# Suite loans_api — the loan READ side (LOAN_SHAPE.md §1, §5, §6): a loan's
# position from the ledger, arrears with reasons, the conditions including
# disagreement. Sourced by mutate.sh. Run with --svc.
#
# No SQL baseline: the schema is the real 0001–0008. The mutants are the named
# breaks of the reference handlers in apifake_loans_test.go.
SUITE_INCLUDE='^TestApiLoans'
SUITE_EXCLUDE='^TestApiLoansWrittenOff'   # its own suite until approved
SUITE_SKIP='^TestApiLoansWrittenOff'
SUITE_RUN='^TestApiLoans'
SVC_RUN='^TestApiLoans'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/loans.go'          # the real handlers, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [loans_borrower_refused]='TestApiLoans1_TheBorrowerReadsTheirLoan'
  [loans_auditor_refused]='TestApiLoans2_AnOfficerAndTheAuditorRead'
  [loans_any_member_reads]='TestApiLoans3_AnotherMemberGetsNotFound'
  [loans_officer_ever]='TestApiLoans4_AFormerOfficerGetsNotFound'
  [loans_officer_any_society]='TestApiLoans5_AnOfficerOfAnotherSocietyGetsNotFound'
  [loans_not_found_says_why]='TestApiLoans3_AnotherMemberGetsNotFound|TestApiLoans4_AFormerOfficerGetsNotFound|TestApiLoans5_AnOfficerOfAnotherSocietyGetsNotFound|TestApiLoans6_TheLoanBookIsForOfficersOnly|TestApiLoans7_AMembersLoansAreTheirsToRead'
  [loans_book_for_members]='TestApiLoans6_TheLoanBookIsForOfficersOnly'
  [loans_member_list_any]='TestApiLoans7_AMembersLoansAreTheirsToRead'
  [loans_outstanding_from_schedule]='TestApiLoans8_OutstandingIsFromTheLedger'
  [loans_posting_basis]='TestApiLoans9_AsOfIsAValueDate'
  [loans_newest_first]='TestApiLoans10_RepaymentsAllocateOldestFirst'
  [loans_part_paid_not_missed]='TestApiLoans11_MissedInstalmentsAndDaysInArrears'
  [loans_reason_count_only]='TestApiLoans12_TheArrearsReasonIsInWords'
  [loans_asof_optional]='TestApiLoans13_AsOfIsRequired'
  [loans_agreed_unchecked]='TestApiLoans14_NotYetDisbursedReadsAgreed'
  [loans_paid_ignored]='TestApiLoans15_OnScheduleReadsActive'
  [loans_settled_reads_early]='TestApiLoans16_ScheduleEndedAndNothingOwedReadsSettled'
  [loans_early_reads_settled]='TestApiLoans17_RepaidEarlyOnAProductThatAllowsItReadsSettledEarly'
  [loans_prepay_terms_ignored]='TestApiLoans18_RepaidEarlyAgainstTheTermsIsADisagreement'
  [loans_underpaid_as_arrears]='TestApiLoans19_ScheduleEndedWithMoneyOwedIsADisagreement'
  [loans_overpaid_clamped]='TestApiLoans20_OverpaidIsADisagreementNotAZero'
  [loans_condition_filter_ignored]='TestApiLoans21_OfficersListTheDisagreements'
  [money_as_numbers]='TestApiLoans22_MoneyIsAStringOnTheWire'
  [nil_slices]='TestApiLoans23_EmptyListsAreArraysNotNull'
)
