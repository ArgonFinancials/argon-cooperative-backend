# Suite requests_api — raise a withdrawal request; the member's list and the
# society's (0016). Sourced by mutate.sh. Run with --svc.
#
# Base: the real 0001–0016, so the fake maps the database's refusals as the
# real handlers will, rather than re-implementing the rules (the 0015 lesson:
# two layers checking the same thing means neither is tested). Until 0016's
# migration existed the base carried its interface and reference rules
# (test/tier1/0016_reference.sql, scaffolding); the guard below refused that
# arrangement once the migration appeared, and fired as designed (2026-10-20).
SUITE_INCLUDE='^TestApiRequests'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiRequests'
SVC_RUN='^TestApiRequests'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/requests.go'          # the real handlers, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [req_step_up_as_500]='TestApiRequests02_RaisingNeedsAStepUp'
  [req_any_officer_raises]='TestApiRequests05_TheAuditorAndTheChairmanDoNotRaise'
  [req_anyone_raises]='TestApiRequests04_AnotherMemberCannotRaiseForYou'
  [req_not_found_says_why]='TestApiRequests06_AnyoneElseGetsNotFound'
  [req_withdrawable_unexplained]='TestApiRequests07_BeyondWithdrawableIsRefusedWithTheFigures'
  [req_key_ignored]='TestApiRequests08_ARetryIsRaisedOnce'
  [req_retry_needs_step_up]='TestApiRequests08_ARetryIsRaisedOnce'
  [req_key_optional]='TestApiRequests09_TheKeyIsRequired'
  [req_resubmission_as_500]='TestApiRequests11_AResubmissionPointsAtTheRejected'
  [req_unreachable_as_500]='TestApiRequests21_ARequestNoQuorumCanApproveIsRefused'
  [req_unreachable_rewritten]='TestApiRequests21_ARequestNoQuorumCanApproveIsRefused'
  [req_raiser_sign_as_500]='TestApiRequests12_TheRaiserNeverSigns'
  [req_raiser_shown_to_member]='TestApiRequests01_AMemberRaisesTheirOwn|TestApiRequests14_TheMembersListNamesNobody'
  [req_member_list_anyone]='TestApiRequests13_TheMemberListsTheirOwnNewestFirst'
  [req_member_list_officer_view]='TestApiRequests13_TheMemberListsTheirOwnNewestFirst|TestApiRequests14_TheMembersListNamesNobody|TestApiRequests15_TheFraudPathInTheMembersList'
  [req_list_oldest_first]='TestApiRequests13_TheMemberListsTheirOwnNewestFirst'
  [req_list_null]='TestApiRequests16_AnEmptyListIsEmpty'
  [req_status_filter_ignored]='TestApiRequests17_OfficersListByStatus'
  [req_society_list_members]='TestApiRequests19_AMemberCannotListTheSociety'
  [money_as_numbers]='TestApiRequests20_MoneyIsAStringOnTheWire'
  [req_auditor_refused]='TestApiRequests18_TheAuditorReadsTheSocietysList'
  [req_society_list_member_view]='TestApiRequests17_OfficersListByStatus'
  [req_officer_member_view]='TestApiRequests03_AnOfficerRaisesForAMember'
)
if [[ -f "$REPO_DIR/migrations/0016_withdrawal_requests.up.sql" && $SUITE_BASE == *0016_reference.sql* ]]; then
  die "suite requests_api: 0016's migration exists, and this suite's base still names its interface and reference. Move SUITE_BASE to migrations/0016_withdrawal_requests.up.sql (the temporary arrangement has outlived its reason)."
fi
