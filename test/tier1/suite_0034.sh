# Suite 0034 — officers enrol by email (2026-11-20). M0 is the email and the
# emailed code with every rule off (0034_interface.sql, generated with the
# mutants from the migration: test/tier1/gen0034.py).
SUITE_INCLUDE='^TestEmail'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestEmail'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,test/tier1/0034_interface.sql'
SUITE_MUTDIR='mutants_0034'
SUITE_GREEN_NEEDS='migrations/0034_enrol_by_email.up.sql'
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestEmail0[1-9]_|TestEmail10_'
  [m_email_any_setter]='TestEmail01_OnlyTheCurrentSecretarySetsAnEmailOnTheirOwnAct'
  [m_email_any_act]='TestEmail01_OnlyTheCurrentSecretarySetsAnEmailOnTheirOwnAct'
  [m_email_any_case]='TestEmail02_AnEmailIsOneAddressLowerCased'
  [m_email_shape_loose]='TestEmail02_AnEmailIsOneAddressLowerCased'
  [m_email_record_rewritable]='TestEmail03_WhoSetItAndWhenIsRecordedClearingToo'
  [m_email_set_at_unset]='TestEmail03_WhoSetItAndWhenIsRecordedClearingToo'
  [m_slip_window_for_email]='TestEmail04_AnEmailedCodeLasts72HoursASlip30Days'
  [m_email_without_address]='TestEmail05_AnEmailedCodeGoesToTheMembersOwnEmailAndNoOther'
  [m_email_to_asked]='TestEmail05_AnEmailedCodeGoesToTheMembersOwnEmailAndNoOther'
  [m_setter_may_issue]='TestEmail06_WhoSetTheEmailDoesNotIssueTheEmailedCode'
  [m_operator_may_email]='TestEmail07_TheOperatorNeverEmailsACode'
  [m_delivery_mutable]='TestEmail08_HalvesGoTheWayTheFirstWentAndNotFromTheSetter'
  [m_setter_second_half]='TestEmail09_TheSetterIssuesNeitherHalf'
  [m_send_unsaid]='TestEmail10_ASendIsRecordedWithoutTheCodeAndNeverEdited'
  [m_send_editable]='TestEmail10_ASendIsRecordedWithoutTheCodeAndNeverEdited'
  [c_every_code_emailed]='TestEmail_Control_TheSecretarySetsAnotherOfficerEmailsTheSlipStays'
)
