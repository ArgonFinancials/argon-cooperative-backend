# Suite disputes_api — disputes over HTTP (0042, stage 1, 2026-11-25; D-13):
# the member's query, an officer's answer, the member's reply, a correction by
# two officers that names its reversal; every refusal in words. The breaks are
# in the reference (apifake_disputes_test.go). Run with --svc.
SUITE_INCLUDE='^TestApiDisputes'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiDisputes'
SVC_RUN='^TestApiDisputes'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,migrations/0035_demo_societies.up.sql,migrations/0036_founding_officers_active.up.sql,migrations/0037_defaults_said.up.sql,migrations/0038_member_status.up.sql,migrations/0039_vfd_as_proven.up.sql,migrations/0040_member_bank_accounts.up.sql,migrations/0041_coop_reference.up.sql,migrations/0042_disputes.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/disputes.go'
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [disputes_once_unmapped]='TestApiDisputes01_TheMemberQueriesTheirOwnEntry'
  [disputes_question_db]='TestApiDisputes02_AQueryIsInWordsAndAboutTheMembersOwnEntry'
  [disputes_answer_own_unmapped]='TestApiDisputes03_AnOfficerAnswersOnceWhileItWaits'
  [disputes_not_waiting_unmapped]='TestApiDisputes03_AnOfficerAnswersOnceWhileItWaits'
  [disputes_reply_anyone]='TestApiDisputes04_TheMemberAcceptsOrReopensSayingWhy'
  [disputes_note_db]='TestApiDisputes04_TheMemberAcceptsOrReopensSayingWhy'
  [disputes_events_newest_first]='TestApiDisputes04_TheMemberAcceptsOrReopensSayingWhy'
  [disputes_step_up_unmapped]='TestApiDisputes05_ACorrectionNeedsASecondOfficerAndNamesItsReversal'
  [disputes_second_officer_unmapped]='TestApiDisputes05_ACorrectionNeedsASecondOfficerAndNamesItsReversal'
  [disputes_reversal_unsaid]='TestApiDisputes05_ACorrectionNeedsASecondOfficerAndNamesItsReversal'
  [disputes_queue_all]='TestApiDisputes06_TheQueueAndWhoReadsIt'
  [disputes_any_reader]='TestApiDisputes06_TheQueueAndWhoReadsIt'
  [disputes_vfd_unmapped]='TestApiDisputes07_APaymentVFDConfirmedIsNotCorrectedByOfficers'
)
