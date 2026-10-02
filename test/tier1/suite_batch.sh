# Suite batch — postAll: POST /societies/{societyId}/ledger/batches
# (LEDGER_CONTRACT §1). Sourced by mutate.sh.
#
# No SQL baseline: the schema is the real 0001–0004. The mutants are the named
# breaks of the reference handler in apifake_batch_test.go, selected by
# ARGON_TEST_API_FAKE. Run with --svc.
SUITE_INCLUDE='^TestApiBatch'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiBatch'
SVC_RUN='^TestApiBatch'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,test/tier1/0005_interface.sql'
SUITE_MUTDIR='mutants_batch_none'
SUITE_GREEN_NEEDS='internal/api/batch.go'      # the real handler, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [wrong_side]='TestApiBatch01_CommitsEachRowAsABalancedOpeningBalance'
  [unattributed]='TestApiBatch01_CommitsEachRowAsABalancedOpeningBalance'
  [source_line_dropped]='TestApiBatch01_CommitsEachRowAsABalancedOpeningBalance'
  [asof_null_as_today]='TestApiBatch02_UnknownAsOfIsStoredAsNull'
  [per_row_commit]='TestApiBatch03_FailurePartwayWritesNothing'
  [claim_then_write]='TestApiBatch03_FailurePartwayWritesNothing|TestApiBatch04_RetryAfterFailureSucceeds|TestApiBatch08_ClientTimeoutThenRetryPostsExactlyOnce|TestApiBatch12_TotalMismatchRefusesAndDoesNotClaimTheReference'
  [refusal_claims_key]='TestApiBatch12_TotalMismatchRefusesAndDoesNotClaimTheReference'
  [no_idempotency]='TestApiBatch05_SecondCallWritesNothingAndReturnsTheFirstCommit'
  [idempotency_after_validation]='TestApiBatch05_SecondCallWritesNothingAndReturnsTheFirstCommit'
  [asof_from_request]='TestApiBatch05_SecondCallWritesNothingAndReturnsTheFirstCommit'
  [record_from_request]='TestApiBatch05_SecondCallWritesNothingAndReturnsTheFirstCommit'
  [asof_null_reads_as_posting_date]='TestApiBatch06_SecondCallReturnsTheFirstCommitsUnknownAsOf'
  [race_unhandled]='TestApiBatch07_RetryRacingTheOriginalPostsOnce'
  [no_society_lock]='TestApiBatch09_TwoImportsNamingOneMemberCannotBothPost'
  [balance_check_file_only]='TestApiBatch10_MemberWithAnOpeningBalanceRefusesTheWholeFile'
  [reversed_still_counts]='TestApiBatch11_ReversedOpeningBalanceDoesNotBlockAReimport'
  [total_trusted]='TestApiBatch12_TotalMismatchRefusesAndDoesNotClaimTheReference'
  [duplicate_member_ok]='TestApiBatch13_DuplicateMemberInTheFile'
  [foreign_member_ok]='TestApiBatch14_MemberOutsideTheSocietyIsUnknown'
  [unknown_member_says_why]='TestApiBatch14_MemberOutsideTheSocietyIsUnknown'
  [empty_ok]='TestApiBatch15_NothingToPost'
  [zero_written]='TestApiBatch16_AmountsAreMagnitudeStrings'
  [negative_as_magnitude]='TestApiBatch16_AmountsAreMagnitudeStrings'
  [future_ok]='TestApiBatch17_AsOfInTheFutureIsRefused'
  [period_closed_as_500]='TestApiBatch18_AsOfInAClosedPeriodIsRefused'
  [any_officer_imports]='TestApiBatch19_OnlyTheSecretaryImports'
  [auditor_imports]='TestApiBatch19_OnlyTheSecretaryImports'
  [outsider_told_forbidden]='TestApiBatch19_OnlyTheSecretaryImports'
  [former_secretary_imports]='TestApiBatch19_OnlyTheSecretaryImports'
  [ended_session_ok]='TestApiBatch20_NoValidSessionGetsUnauthorized'
  [chart_any_account]='TestApiBatch21_ChartThatCannotTakeTheBatchRefuses'
  [key_global]='TestApiBatch22_ReferenceIsPerSociety'
)
