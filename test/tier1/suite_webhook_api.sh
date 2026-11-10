# Suite webhook_api — VFD's notifications, kept as received and never
# believed (stage 3, 2026-11-10). The breaks are in the reference handler
# (apifake_webhook_test.go), selected by ARGON_TEST_API_FAKE. Run with --svc.
SUITE_INCLUDE='^TestApiWebhook'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestApiWebhook'
SVC_RUN='^TestApiWebhook'
SVC_ENV='ARGON_TEST_API_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,migrations/0022_lockout_by_device.up.sql,migrations/0023_society_founding.up.sql,migrations/0024_enrolment_codes.up.sql,migrations/0025_slip_device.up.sql,migrations/0026_enrolment_halves.up.sql,migrations/0027_founding_officers.up.sql,migrations/0028_vfd_wallet.up.sql,migrations/0029_vfd_inflows.up.sql'
SUITE_MUTDIR='mutants_api_none'
SUITE_GREEN_NEEDS='internal/api/webhook.go'
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [webhook_signature_optional]='TestApiWebhook02_SignedModeHasNoUnsignedPath'
  [webhook_signature_ignored]='TestApiWebhook02_SignedModeHasNoUnsignedPath'
  [webhook_credits]='TestApiWebhook03_NothingIsCreditedAtTheDoor'
  [webhook_off_open]='TestApiWebhook04_OffTakesNothing'
  [webhook_no_limit]='TestApiWebhook05_ATooLargeBodyIsRefusedAndNotKept'
  [webhook_mode_defaulted]='TestApiWebhook06_TheConfigurationNamesTheMode'
  [webhook_rewritten]='TestApiWebhook01_ANotificationIsKeptExactlyAsReceivedBeforeTheAnswer'
)
