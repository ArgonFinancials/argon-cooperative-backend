# Suite enrol_api — enrolment over HTTP (stage 1, 2026-11-10): redeeming a
# code (public), an officer issuing one on their step-up (a signatory's in
# two halves, 0026), and the history an officer reads. Sourced by mutate.sh.
# Run with --svc. The breaks are in the reference handlers
# (apifake_enrol_test.go).
SUITE_INCLUDE='^TestApiEnrol'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiEnrol'
SVC_RUN='^TestApiEnrol'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/enrolment.go'      # the real handlers, written after the tests are red
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [enrol_not_public]='TestApiEnrol01_AnOfficerIssuesAWholeSlipAndTheMemberEnrols'
  [enrol_wrong_code_told]='TestApiEnrol08_OneRefusalForEveryWrongThing'
  [enrol_slip_used_as_refused]='TestApiEnrol09_EachRefusalSaysWhatToDo'
  [enrol_refusals_as_500]='TestApiEnrol09_EachRefusalSaysWhatToDo'
  [enrol_lock_as_refused]='TestApiEnrol10_ALockedAccountIsToldToWait'
  [enrol_browser_no_cookies]='TestApiEnrol11_ABrowserEnrolsIntoCookies'
  [issue_signatory_whole]='TestApiEnrol03_ASignatorysCodeIsTwoHalvesFromTwoSignatories'
  [issue_same_signatory_db]='TestApiEnrol04_TheSameSignatoryCannotIssueTheOtherHalf'
  [issue_second_sees_first]='TestApiEnrol03_ASignatorysCodeIsTwoHalvesFromTwoSignatories'
  [issue_refusals_as_500]='TestApiEnrol05_WhoMayNotIssue'
  [issue_outsider_reaches_db]='TestApiEnrol06_SomeoneWhoIsNotAnOfficerHereIsToldNothing'
  [issue_member_any_society]='TestApiEnrol06_SomeoneWhoIsNotAnOfficerHereIsToldNothing'
  [history_shows_digest]='TestApiEnrol02_TheSlipIsShownOnce'
  [history_issuer_unnamed]='TestApiEnrol07_TheHistoryNamesIssuerTimeAndDevice'
  [history_no_device]='TestApiEnrol07_TheHistoryNamesIssuerTimeAndDevice'
  [history_oldest_first]='TestApiEnrol07_TheHistoryNamesIssuerTimeAndDevice'
)
