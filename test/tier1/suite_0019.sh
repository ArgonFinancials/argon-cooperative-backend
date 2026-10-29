# Suite 0019 — every sign-in code handed to the SMS provider is recorded:
# sms_sends (the Termii sender, piece 3, approved 2026-10-28). Sourced by mutate.sh.
SUITE_INCLUDE='^TestSmsSend'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestSmsSend'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,test/tier1/0019_interface.sql'
SUITE_MUTDIR='mutants_0019'
SUITE_GREEN_NEEDS='migrations/0019_sms_sends.up.sql'   # absent until the red half is proven
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  # M0: the rules 01-05. Test 06 checks the table's SHAPE, which the interface has;
  # m_sms_text_column proves it.
  [M0]='TestSmsSend0[1-5]'
  [m_sms_id_optional]='TestSmsSend01_AnAcceptedSendCarriesItsMessageId|TestSmsSend02_OnlyAnAcceptedSendCarriesAMessageId'
  [m_sms_updatable]='TestSmsSend03_ASendIsWrittenOnceAndNeverChanged'
  [m_sms_id_twice]='TestSmsSend04_AMessageIdIsRecordedOnce'
  [m_sms_code_anywhere]='TestSmsSend05_ARefusalCodeOnlyOnARefusal'
  [m_sms_text_column]='TestSmsSend06_ThereIsNoColumnForTheTextOrTheCode'
  [c_sms_no_unknown]='TestSmsSend_Control_EachOutcomeIsRecorded'
)
