# Suite wallet — the wallet worker (stage 3, 2026-11-10): the last approval
# pays. Against a fake VFD in the test; GREEN as argon_wallet with the real
# grants. The breaks are in the reference worker (walletfake_test.go, generated
# from internal/wallet/wallet.go), selected by ARGON_TEST_WALLET_FAKE. Run
# with --svc.
SUITE_INCLUDE='^TestWalletWorker'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestWalletWorker'
SVC_RUN='^TestWalletWorker'
SVC_ENV='ARGON_TEST_WALLET_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql'
SUITE_MUTDIR='mutants_wallet_none'
SUITE_GREEN_NEEDS='internal/wallet/wallet.go'
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [pays_officer_holds]='TestWalletWorker02_AnOfficersHoldIsNotTheSystemsToPay'
  [no_enquiry]='TestWalletWorker03_NoTransferBeforeVFDConfirmsTheAccount'
  [refused_waits]='TestWalletWorker04_ARefusedSendFailsAndTheHoldIsReleased'
  [resend_unknown]='TestWalletWorker05_ATimeoutIsNotAFailureAndNothingIsSentTwice'
  [release_on_unknown]='TestWalletWorker05_ATimeoutIsNotAFailureAndNothingIsSentTwice'
  [lost_resent]='TestWalletWorker06_ALostSendIsAskedAboutNeverResent'
  [lost_ignored]='TestWalletWorker06_ALostSendIsAskedAboutNeverResent'
  [notfound_fails_now]='TestWalletWorker07_NotFoundFailsOnlyAfterTheWindow'
  [unreadable_fails]='TestWalletWorker07_NotFoundFailsOnlyAfterTheWindow'
  [success_unrecorded]='TestWalletWorker01_TheLastSignaturesHoldIsPaidAndPosted'
  [body_amount_believed]='TestWalletWorker08_AnInflowIsVFDsAmountNotTheNotifications'
  [unconfirmed_believed]='TestWalletWorker09_NothingIsRecordedWhileVFDDoesNotSaySucceeded'
  [unmatched_unsaid]='TestWalletWorker11_MoneyToNoSocietysAccountIsUnmatched'
  [dup_attempt_unrecorded]='TestWalletWorker10_ANotificationDeliveredTwiceIsRecordedOnce'
  [reconcile_silent]='TestWalletWorker12_ReconciliationRecordsThatItCouldNotRun'
  [reconcile_matched]='TestWalletWorker12_ReconciliationRecordsThatItCouldNotRun'
)
