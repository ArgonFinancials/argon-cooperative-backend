# Suite members_api — the register over HTTP (2026-11-10): find, branches,
# register one, the import, balances by member number. The breaks are in the
# reference handlers (apifake_members_test.go, apifake_batch_test.go),
# selected by ARGON_TEST_API_FAKE. Run with --svc.
SUITE_INCLUDE='^TestApiMembers'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiMembers'
SVC_RUN='^TestApiMembers'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/register.go'   # the real handlers, written after the red half
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [members_any_reader]='TestApiMembers01_TheRegisterIsForTheSocietysOfficers'
  [members_phone_unnormalized]='TestApiMembers02_FoundByNumberPhoneOrName'
  [members_enrolment_blind]='TestApiMembers03_TheSummarySaysPostsEnrolmentAndBalance'
  [members_balance_blind]='TestApiMembers03_TheSummarySaysPostsEnrolmentAndBalance'
  [branches_any_reader]='TestApiMembers04_BranchesAreReadByOfficers'
  [register_phone_raw]='TestApiMembers05_TheSecretaryRegistersOneInPlusTwoThreeFourForm'
  [register_refusal_as_500]='TestApiMembers06_OneMemberRefusedSaysWhy'
  [import_partial]='TestApiMembers07_AnImportIsAllOrNothingAndSaysEveryRefusedLine'
  [import_first_refusal_only]='TestApiMembers07_AnImportIsAllOrNothingAndSaysEveryRefusedLine'
  [import_key_ignored]='TestApiMembers08_AnImportRegistersEveryRowOnceAndAnswersTheirIds'
  [secretary_imports]='TestApiMembers09_TheTreasurerPostsBalancesByMemberNumber'
  [number_any_society]='TestApiMembers10_AMemberNumberIsResolvedInThisSocietyOnly'
)
