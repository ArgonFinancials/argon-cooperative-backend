# Suite email_api — officers enrol by email, over HTTP (0034, 2026-11-20): the
# secretary records the address; the code goes to it and never to the issuing
# officer; whoever set the address does not email to it; a failed send issues
# nothing; no sender says so; the halves go one way; the history and the
# register say it; the sender is configured, never defaulted. The breaks are
# in the reference handlers (apifake_enrol_test.go, apifake_members_test.go,
# apifake_session_test.go), selected by ARGON_TEST_API_FAKE. Run with --svc.
SUITE_INCLUDE='^TestApiEmail'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiEmail'
SVC_RUN='^TestApiEmail'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql,migrations/0030_slip_used.up.sql,migrations/0031_member_register.up.sql,migrations/0032_contribution_claims.up.sql,migrations/0033_payment_resolutions.up.sql,migrations/0034_enrol_by_email.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/email/email.go'   # the real handlers, written with the tests
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [set_email_any_officer]='TestApiEmail01_TheSecretarySetsTheAddressAndTheSummarySaysWho'
  [set_email_bad_as_500]='TestApiEmail01_TheSecretarySetsTheAddressAndTheSummarySaysWho'
  [email_setter_hidden]='TestApiEmail01_TheSecretarySetsTheAddressAndTheSummarySaysWho'
  [email_slip_shown]='TestApiEmail02_TheCodeGoesToTheMemberAndNotToTheOfficer'
  [email_unmasked]='TestApiEmail02_TheCodeGoesToTheMemberAndNotToTheOfficer'
  [email_setter_db]='TestApiEmail03_TheOfficerWhoSetTheAddressDoesNotEmailTheCode'
  [email_no_address_db]='TestApiEmail04_NoAddressIsToldAndNothingIsIssued'
  [email_issued_unsent]='TestApiEmail05_ASendThatFailsIssuesNothingAndIsRecorded'
  [email_unavailable_500]='TestApiEmail06_NoSenderSaysSoAndTheSlipStillWorks'
  [history_email_always]='TestApiEmail06_NoSenderSaysSoAndTheSlipStillWorks'
  [email_mismatch_unchecked]='TestApiEmail07_ASignatorysHalvesBothGoByEmail'
  [history_sent_to_unmasked]='TestApiEmail08_TheHistorySaysHowEachCodeWent'
  [register_email_dropped]='TestApiEmail09_TheRegisterTakesAnAddress'
  [email_mode_defaulted]='TestApiEmail10_TheConfigurationNamesTheSender'
  [ses_region_optional]='TestApiEmail10_TheConfigurationNamesTheSender'
)
