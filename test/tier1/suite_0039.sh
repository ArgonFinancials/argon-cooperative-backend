# Suite 0039 — VFD as production proved it (2026-11-24, docs/VFD_INTEGRATION.md
# §1.6). M0 is the migration with every rule off (0039_interface.sql, generated
# with the mutants: test/tier1/gen0039.py).
SUITE_INCLUDE='^TestVfdRules'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestVfdRules'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,migrations/0035_demo_societies.up.sql,migrations/0036_founding_officers_active.up.sql,migrations/0037_defaults_said.up.sql,migrations/0038_member_status.up.sql,test/tier1/0039_interface.sql'
SUITE_MUTDIR='mutants_0039'
SUITE_GREEN_NEEDS='migrations/0039_vfd_as_proven.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestVfdRules0[1-7]_'
  [m_paid_on_any_send]='TestVfdRules01_PaidOnTheSends00OrARequerys00With00'
  [m_paid_any_txn]='TestVfdRules01_PaidOnTheSends00OrARequerys00With00'
  [m_failed_once]='TestVfdRules02_FailedOnlyOnOneOhEightTwicePastThirtyMinutes'
  [m_failed_first_early]='TestVfdRules02_FailedOnlyOnOneOhEightTwicePastThirtyMinutes'
  [m_failed_any_code]='TestVfdRules02_FailedOnlyOnOneOhEightTwicePastThirtyMinutes'
  [m_failed_on_send]='TestVfdRules02_FailedOnlyOnOneOhEightTwicePastThirtyMinutes'
  [m_from_anywhere]='TestVfdRules03_ATransferLeavesFromThePool'
  [m_pool_twice]='TestVfdRules04_ThePoolIsRecordedOnceAndNeverEdited'
  [m_pool_editable]='TestVfdRules04_ThePoolIsRecordedOnceAndNeverEdited'
  [m_needs_vfd_unseen]='TestVfdRules05_AnythingElseVFDSaysIsShownForAPerson'
  [m_posts_only_vfds_amount]='TestVfdRules06_PaidOnTheSendPostsWhatWasSent'
  [m_source_optional]='TestVfdRules07_ANotificationSaysWhereItCameFrom'
  [m_source_any]='TestVfdRules07_ANotificationSaysWhereItCameFrom'
  [c_never_failed]='TestVfdRules_Control_OneOhEightTwicePastThirtyMinutesReleases'
)
