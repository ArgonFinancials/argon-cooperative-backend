# Suite loans_api_wo — the loan read side's WRITTEN OFF condition: a reviewed
# addition to suite loans_api, kept apart until approved and built so the
# approved suite stays green; then merged into it. Run with --svc.
SUITE_INCLUDE='^TestApiLoansWrittenOff'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiLoansWrittenOff'
SVC_RUN='^TestApiLoansWrittenOff'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/loans_written_off.go'   # the handler change, after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [loans_written_off_reads_settled]='TestApiLoansWrittenOff1_AWrittenOffLoanSaysSoAndWhy|TestApiLoansWrittenOff2_ARecoveryIsReportedNotRepaid'
  [loans_write_off_reason_dropped]='TestApiLoansWrittenOff1_AWrittenOffLoanSaysSoAndWhy'
  [loans_recovery_as_repayment]='TestApiLoansWrittenOff2_ARecoveryIsReportedNotRepaid'
  [loans_write_off_ignores_date]='TestApiLoansWrittenOff3_AWriteOffIsAsAtItsValueDate'
)
