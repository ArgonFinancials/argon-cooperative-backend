# Suite 0029 — money arriving (stage 3, 2026-11-10): notifications kept,
# inflows confirmed once, reconciliation that says when it could not run, and
# what a person must act on. M0 is the shape with no rules (0029_interface.sql).
SUITE_INCLUDE='^TestWalletInflow'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestWalletInflow'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,test/tier1/0029_interface.sql'
SUITE_MUTDIR='mutants_0029'
SUITE_GREEN_NEEDS='migrations/0029_vfd_inflows.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestWalletInflow0[1-8]'
  [m_notification_edited]='TestWalletInflow01_ANotificationIsKeptAsReceived'
  [m_any_kind]='TestWalletInflow01_ANotificationIsKeptAsReceived'
  [m_mute_attempt]='TestWalletInflow02_AnAttemptSaysWhyItDidNotConfirm'
  [m_any_result]='TestWalletInflow02_AnAttemptSaysWhyItDidNotConfirm'
  [m_attempt_edited]='TestWalletInflow02_AnAttemptSaysWhyItDidNotConfirm'
  [m_inflow_twice]='TestWalletInflow03_AnInflowIsRecordedOncePerVFDReference'
  [m_any_account]='TestWalletInflow04_AnInflowLandsInTheSocietysOwnAccount'
  [m_inflow_zero]='TestWalletInflow05_AnInflowIsMoneyArrivingAndNeverEdited'
  [m_inflow_edited]='TestWalletInflow05_AnInflowIsMoneyArrivingAndNeverEdited'
  [m_mute_run]='TestWalletInflow06_AReconciliationSaysWhatItFoundOverARealPeriod'
  [m_any_period]='TestWalletInflow06_AReconciliationSaysWhatItFoundOverARealPeriod'
  [m_no_stuck]='TestWalletInflow07_WhatAPersonMustActOnIsSeen'
  [m_no_failed]='TestWalletInflow07_WhatAPersonMustActOnIsSeen'
  [m_no_unconfirmed]='TestWalletInflow07_WhatAPersonMustActOnIsSeen'
  [m_no_unclaimed]='TestWalletInflow07_WhatAPersonMustActOnIsSeen'
  [m_no_unrun]='TestWalletInflow07_WhatAPersonMustActOnIsSeen'
  [m_no_hold_not_sent]='TestWalletInflow08_AHoldThatNeverWentOutIsSeen'
  [m_hold_not_sent_at_once]='TestWalletInflow08_AHoldThatNeverWentOutIsSeen'
  [c_settled_shown]='TestWalletInflow_Control_WhatIsSettledIsNotAnException'
)
