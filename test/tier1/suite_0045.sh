# Suite 0045 — a forgotten PIN and a changed phone (2026-11-28, the reviewer's
# decisions; LEDGER_CONTRACT §12). M0: test/tier1/0045_interface.sql (gen0045.py).
SUITE_INCLUDE='^TestResetPhone'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestResetPhone'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql,migrations/0035_demo_societies.up.sql,migrations/0036_founding_officers_active.up.sql,migrations/0037_defaults_said.up.sql,migrations/0038_member_status.up.sql,migrations/0039_vfd_as_proven.up.sql,migrations/0040_member_bank_accounts.up.sql,migrations/0041_coop_reference.up.sql,migrations/0042_disputes.up.sql,migrations/0043_corrections.up.sql,migrations/0044_period_close.up.sql,test/tier1/0045_interface.sql'
SUITE_MUTDIR='mutants_0045'
SUITE_GREEN_NEEDS='migrations/0045_resets_and_phone_changes.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestResetPhone(0[235-9]|1[0-2])_'
  [m_reset_any_officer]='TestResetPhone02_OnlyTheSecretaryResetsAMember'
  [m_officer_one_reset]='TestResetPhone03_AnOfficersPINIsNeverOneOfficersEvenANonSigningOne'
  [m_request_signing_only]='TestResetPhone04_TwoOtherSignatoriesResetAnOfficer'
  [m_approval_silent]='TestResetPhone05_EachApprovingSignatoryStatesTheirReason'
  [m_phone_any_officer]='TestResetPhone06_OnlyTheSecretaryChangesANumber'
  [m_phone_absent_ok]='TestResetPhone07_TheMemberIsPresentAndTheActSaysSo'
  [m_officer_phone_one]='TestResetPhone08_AnOfficersNumberNeedsTwoEvenANonSigningOne'
  [m_request_absent_ok]='TestResetPhone08_AnOfficersNumberNeedsTwoEvenANonSigningOne'
  [m_notice_missing]='TestResetPhone09_TheOldNumberIsToldWhoChangedIt'
  [m_notice_one_path]='TestResetPhone10_TheOldNumberIsToldOnTheTwoSignatoryPathToo'
  [m_sent_twice]='TestResetPhone11_WhatCameOfTheNoticeIsRecordedOnce'
  [m_editable]='TestResetPhone12_NoticesAndWhatCameOfThemAreNeverEdited'
  [c_no_member_reset]='TestResetPhone_Control_TheSecretaryResetsAMemberAndChangesTheirNumber'
)
