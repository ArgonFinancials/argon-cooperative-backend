# Suite api — GET /members/{memberId}/statement. Sourced by mutate.sh.
#
# No SQL baseline: the schema is real (0001–0003) plus the 0004 interface. The
# mutants are the 20 named breaks of the reference handler in apifake_test.go,
# selected by ARGON_TEST_API_FAKE. Run with --svc.
SUITE_INCLUDE='^TestApiStatement'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiStatement'
SVC_RUN='^TestApiStatement'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,test/tier1/0004_interface.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/statement.go'      # the real handler, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [member_sees_attribution]='TestApiStatement1_MemberReadsOwnStatementWithoutAttribution'
  [member_refused]='TestApiStatement1_MemberReadsOwnStatementWithoutAttribution'
  [officer_unattributed]='TestApiStatement2_OfficerSeesWhoPosted'
  [two_queries]='TestApiStatement3_ProjectionsAreOneRecord'
  [auditor_refused]='TestApiStatement4_AuditorReadsTheOfficerProjection'
  [charges_summed]='TestApiStatement5_ChargesAreTheirOwnLinesPartnerAndMarginApart'
  [refund_as_charge]='TestApiStatement6_RefundIsNotASecondCharge'
  [value_date_filled]='TestApiStatement7_UnknownValueDateIsSentAsNull'
  [balance_contract_sign]='TestApiStatement8_BalancesAreFromTheMembersSide'
  [direction_from_bank]='TestApiStatement9_DirectionComesFromTheSavingsLine'
  [unordered]='TestApiStatement10_RowsInPostingOrder'
  [money_as_numbers]='TestApiStatement11_MoneyIsAStringOnTheWire'
  [nil_slices]='TestApiStatement12_EmptyListsAreArraysNotNull'
  [period_ignored]='TestApiStatement13_PeriodBoundsRowsAndCarriesTheBalance'
  [leak_other_lines]='TestApiStatement14_AnotherMembersLineNeverLeaks'
  [any_member_reads]='TestApiStatement15_AnotherMemberGetsNotFound'
  [officer_any_society]='TestApiStatement16_OfficerOfAnotherSocietyGetsNotFound'
  [officer_ever]='TestApiStatement17_FormerOfficerGetsNotFound'
  [not_found_says_why]='TestApiStatement15_AnotherMemberGetsNotFound|TestApiStatement16_OfficerOfAnotherSocietyGetsNotFound|TestApiStatement17_FormerOfficerGetsNotFound'
  [ended_session_ok]='TestApiStatement18_NoValidSessionGetsUnauthorized'
)
