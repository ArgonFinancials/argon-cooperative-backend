# Suite locks_api — an officer releases a locked-out member over HTTP (0022,
# decided 2026-11-05; shipped with the lockout split, never after it).
# Sourced by mutate.sh. Run with --svc. The rules are the database's; the
# breaks are in the reference handler (apifake_locks_test.go).
SUITE_INCLUDE='^TestApiLocks'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiLocks'
SVC_RUN='^TestApiLocks'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/locks.go'      # the real handler, written after the tests
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [lock_step_up_as_500]='TestApiLocks02_ReleasingNeedsTheOfficersOwnStepUp'
  [lock_auditor_as_500]='TestApiLocks03_TheAuditorCannotRelease'
  [lock_self_as_500]='TestApiLocks04_NobodyReleasesTheirOwnLock'
  [lock_outsider_reaches_db]='TestApiLocks05_SomeoneWhoIsNotAnOfficerHereIsToldNothing'
  [lock_member_any_society]='TestApiLocks06_TheMemberIsTheSocietys'
  [lock_nothing_locked_as_500]='TestApiLocks07_AReleaseNeedsALock'
  [lock_reason_as_500]='TestApiLocks08_AReleaseStatesItsReason'
  [lock_releaser_is_member]='TestApiLocks01_AnOfficerReleasesALockedOutMember'
)
