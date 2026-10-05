# Suite books — the society's books (LEDGER_CONTRACT §5): chart of accounts,
# trial balance, general ledger, journal. Sourced by mutate.sh. Run with --svc.
#
# No SQL baseline: the schema is the real 0001–0005. The mutants are the named
# breaks of the reference handlers in apifake_books_test.go, selected by
# ARGON_TEST_API_FAKE.
SUITE_INCLUDE='^TestApiBooks'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiBooks'
SVC_RUN='^TestApiBooks'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/books.go'          # the real handlers, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [books_auditor_refused]='TestApiBooks1_AuditorReadsTheBooks'
  [books_member_reads]='TestApiBooks2_MemberOfTheSocietyGetsNotFound'
  [books_officer_any_society]='TestApiBooks3_OfficerOfAnotherSocietyGetsNotFound'
  [books_officer_ever]='TestApiBooks4_FormerOfficerGetsNotFound'
  [books_not_found_says_why]='TestApiBooks2_MemberOfTheSocietyGetsNotFound|TestApiBooks3_OfficerOfAnotherSocietyGetsNotFound|TestApiBooks4_FormerOfficerGetsNotFound'
  [books_chart_by_name]='TestApiBooks5_ChartIsOrderedByCode'
  [books_chart_all_postable]='TestApiBooks6_ChartSaysWhichAccountsArePostable'
  [books_chart_all_societies]='TestApiBooks7_ChartIsTheSocietysOwn'
  [books_tb_posting_basis]='TestApiBooks8_TrialBalanceIsAsAtTheValueDate'
  [books_tb_undated_excluded]='TestApiBooks9_UndatedOpeningBalanceCountsAtEveryDate'
  [books_tb_asof_ignored]='TestApiBooks10_EntriesAfterAsOfAreLeftOut'
  [books_tb_quiet_accounts_dropped]='TestApiBooks11_EveryAccountOfTheChartIsListed'
  [books_tb_netted]='TestApiBooks12_DebitsAndCreditsAreTurnoverNotNetted'
  [books_tb_credits_unsigned]='TestApiBooks13_CreditsAreSignedAndTotalsSumToZero'
  [books_asof_optional]='TestApiBooks14_AsOfIsRequired'
  [books_gl_posting_order]='TestApiBooks15_LedgerIsInValueDateOrder'
  [books_gl_undated_last]='TestApiBooks16_UndatedRowsComeFirst'
  [books_gl_opening_ignored]='TestApiBooks17_OpeningBalanceCarriesWhatCameBeforeFrom'
  [books_gl_all_lines]='TestApiBooks18_AmountIsThisAccountsLinesOnly'
  [books_gl_to_by_posting]='TestApiBooks19_ClosingBalanceAgreesWithTheTrialBalance'
  [books_gl_unknown_account_ok]='TestApiBooks20_UnknownAccountIsNotFound'
  [books_j_all_societies]='TestApiBooks21_JournalIsTheSocietysOwnInValueDateOrder'
  [books_j_reference_ignored]='TestApiBooks22_ReferenceFindsAnImport'
  [books_j_member_ignored]='TestApiBooks23_MemberFilterFindsTheirEntries'
  [books_j_actor_ignored]='TestApiBooks24_ActorFilterFindsWhoPosted'
  [books_j_filters_or]='TestApiBooks25_FiltersCombine'
  [books_j_charges_dropped]='TestApiBooks26_EveryEntrysLinesSumToZero'
  [books_j_reversed_by_missing]='TestApiBooks27_ReversedByIsDerived'
  [books_j_lines_by_code_desc]='TestApiBooks28_LinesDebitsFirstThenByCode'
  [books_j_undated_after_from]='TestApiBooks29_FromLeavesOutUndatedEntries'
  [money_as_numbers]='TestApiBooks30_MoneyIsAStringOnTheWire'
  [nil_slices]='TestApiBooks31_EmptyListsAreArraysNotNull'
)
