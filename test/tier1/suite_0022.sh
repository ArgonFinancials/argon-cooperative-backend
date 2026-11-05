# Suite 0022 — the lockout, split by device (decided 2026-11-05; findings 60
# and 62). M0 is today's single lock under the new function's name.
SUITE_INCLUDE='^TestDeviceLock'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestDeviceLock'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,migrations/0010_sign_step_up.up.sql,migrations/0011_money_movements.up.sql,migrations/0012_payout_holds.up.sql,migrations/0013_vat_on_argon_fee.up.sql,migrations/0014_operator_settings.up.sql,migrations/0015_benefit_and_duties.up.sql,migrations/0016_withdrawal_requests.up.sql,migrations/0017_quotes_and_largest.up.sql,migrations/0018_member_position.up.sql,migrations/0019_sms_sends.up.sql,migrations/0020_account_role_once.up.sql,migrations/0021_decided_requests.up.sql,test/tier1/0022_interface.sql'
SUITE_MUTDIR='mutants_0022'
SUITE_GREEN_NEEDS='migrations/0022_lockout_by_device.up.sql'   # written after the red half
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestDeviceLock01|TestDeviceLock10|TestDeviceLock02|TestDeviceLock03|TestDeviceLock04|TestDeviceLock05|TestDeviceLock08|TestDeviceLock09|TestDeviceLock11'
  [m_own_window_48h]='TestDeviceLock01_TheOfficerLockOutlastsTwoDays'
  [m_one_lock_for_both]='TestDeviceLock02_AStrangersFailuresNeverLockTheMembersOwnPhone|TestDeviceLock09_OneLockNeverResetsTheOther'
  [m_stranger_reaches_officer]='TestDeviceLock03_AStrangerReachesTimedLocksOnly'
  [m_stranger_window_48h]='TestDeviceLock04_TheStrangerLockLastsItsFullFortyEightHours'
  [m_unknown_number_officer]='TestDeviceLock05_AnUnknownNumberLocksExactlyLikeAStranger'
  [c_own_never_officer]='TestDeviceLock06_Control_TheMembersOwnPhoneStillReachesTheOfficer'
  [m_release_own_only]='TestDeviceLock07_AReleaseClearsBothLocks'
  [m_release_keeps_history]='TestDeviceLock08_AFailureAfterAReleaseStartsANewCount'
  [m_release_anytime]='TestDeviceLock11_AReleaseNeedsALock'
  [m_own_anchor_now]='TestDeviceLock10_TheOfficerLockLastsWithNoSuccessToMeasureFrom'
)
