# Suite money_api — record a contribution, authorise a payout (the hold),
# release a hold, record a payout, over HTTP (0011, 0012). Sourced by
# mutate.sh. Run with --svc.
#
# Base: the real 0001–0014 and 0015's interface (the client reference's column;
# 0015's rules are checked by the fake until the migration exists, then the base
# moves to the real 0015). The rules are proven at the database in suites
# 0011–0013; here, what the API adds: who may, the step-up, the server's
# charges, refusals that carry the database's reasons, and flags in the answer.
SUITE_INCLUDE='^TestApiMoney'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiMoney'
SVC_RUN='^TestApiMoney'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,test/tier1/0015_interface.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/money.go'          # the real handlers, written after review
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [money_acts_unmintable]='TestApiMoney01_EveryMoneyActCanBeSteppedUpFor'
  [money_flags_null]='TestApiMoney02_AContributionIsRecordedOnTheRecordersStepUp|TestApiMoney20_APayoutOnItsHoldIsRecorded'
  [money_step_up_as_500]='TestApiMoney03_AContributionNeedsAStepUp|TestApiMoney12_AHoldNeedsAStepUp|TestApiMoney21_APayoutNeedsAStepUp'
  [money_charge_not_the_servers]='TestApiMoney04_TheChargeIsTheServers'
  [money_member_amount_gross]='TestApiMoney04_TheChargeIsTheServers|TestApiMoney20_APayoutOnItsHoldIsRecorded'
  [money_undetermined_as_zero]='TestApiMoney05_AnUndeterminedChargeIsRecordedAndSaysSo'
  [money_flags_dropped]='TestApiMoney05_AnUndeterminedChargeIsRecordedAndSaysSo|TestApiMoney22_ADifferentAmountIsRecordedAndFlagged|TestApiMoney23_APayoutWithNoHoldIsRecordedAndFlagged|TestApiMoney26_APayoutOnAReleasedHoldIsRecordedAndFlagged'
  [money_any_officer_records]='TestApiMoney06_RecordingIsTheTreasurersOrTheSecretarys|TestApiMoney27_RecordingAPayoutIsTheTreasurersOrTheSecretarys'
  [money_post_fixed]='TestApiMoney06_RecordingIsTheTreasurersOrTheSecretarys|TestApiMoney27_RecordingAPayoutIsTheTreasurersOrTheSecretarys'
  [money_not_found_says_why]='TestApiMoney07_AnyoneElseGetsNotFound'
  [money_member_any_society]='TestApiMoney08_TheMemberIsThisSocietys'
  [money_as_numbers]='TestApiMoney09_MoneyIsAStringOnTheWire'
  [money_zero_ok]='TestApiMoney10_AZeroContributionIsRefused'
  [money_hold_charges_unread]='TestApiMoney11_AHoldCarriesTheServersCharges'
  [money_refusal_unexplained]='TestApiMoney13_OnlyAnApprovedWithdrawalIsAuthorised|TestApiMoney15_BeyondWithdrawableIsRefusedWithTheFigures|TestApiMoney17_AnUnsetFloorIsNamed'
  [money_hold_open_as_approved]='TestApiMoney14_OneOpenHoldPerRequest'
  [money_withdrawable_as_409]='TestApiMoney15_BeyondWithdrawableIsRefusedWithTheFigures'
  [money_any_officer_authorises]='TestApiMoney16_AuthorisingIsASignatorysDuty'
  [money_not_configured_as_500]='TestApiMoney17_AnUnsetFloorIsNamed'
  [money_release_no_reason]='TestApiMoney18_AReleaseSaysWhy'
  [money_released_state_open]='TestApiMoney18_AReleaseSaysWhy'
  [money_paid_release_as_500]='TestApiMoney19_APaidHoldIsNeverReleased'
  [money_payout_charges_dropped]='TestApiMoney20_APayoutOnItsHoldIsRecorded'
  [money_duplicate_unnamed]='TestApiMoney24_ATransferIsRecordedOnce'
  [money_duplicate_as_500]='TestApiMoney24_ATransferIsRecordedOnce'
  [money_hold_member_as_500]='TestApiMoney25_AHeldPayoutIsTheHoldsMembers'
  [money_reference_optional]='TestApiMoney28_APayoutNamesItsTransfer'
  [money_retry_records_twice]='TestApiMoney29_ARetriedContributionIsRecordedOnce'
  [money_retry_needs_step_up]='TestApiMoney29_ARetriedContributionIsRecordedOnce'
  [money_key_optional]='TestApiMoney30_AContributionCarriesItsIdempotencyKey'
  [money_self_recording_ok]='TestApiMoney31_NobodyRecordsTheirOwnContribution|TestApiMoney33_NobodyRecordsTheirOwnPayout'
  [money_self_authorising_ok]='TestApiMoney32_NobodyAuthorisesTheirOwnPayout'
  [money_authoriser_records_ok]='TestApiMoney34_TheAuthoriserNeverRecordsThePayout'
)
