# Suite 0003 — authentication. Sourced by mutate.sh.
#
# Database tests (TestAuth0..H) run against 0001 + 0002 + the 0003 interface,
# plus one SQL mutant from mutants_0003/. Service tests (TestAuthS*) run
# against the fake service in authfake_test.go, broken one named way per run.
SUITE_INCLUDE='^TestAuth'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestAuth[0A-H]'           # database tests; service tests run under --svc
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,test/tier1/0003_interface.sql'
SUITE_MUTDIR='mutants_0003'
SUITE_GREEN_NEEDS='migrations/0003_auth.up.sql'
SUITE_GREEN_RUN='^TestAuth'                      # GREEN runs database AND service tests, against the REAL service
CONTROL_RE='Control|^TestAuth0_'                   # must PASS on M0

# Service breaks => the TestAuthS* tests each must turn red. See authfake_test.go.
declare -gA SVC_TARGET_OF=(
  [distinct_unknown]='TestAuthS1_UnknownNumberAndWrongPinRefuseIdentically'
  [timing_fast_unknown]='TestAuthS3_RefusalTimingDoesNotRevealRegistration'
  [lock_real_only]='TestAuthS2_LockoutLooksTheSameForUnknownNumbers'
  [per_credential_form]='TestAuthS9_CredentialFormsShareOneCount'
  [code_in_challenge]='TestAuthS6_CodeNeverAppearsInAResponse'
  [no_notify]='TestAuthS7_OtherDevicesAreToldOfANewOne'
  [trust_client_token]='TestAuthS8_ClientAssertedDeviceTokenIsANewDevice'
  [sms_first_record]='TestAuthS10_NoAgreedNumberRoutesToAnOfficer'
  [always_code]='TestAuthS4_KnownDeviceSignsInWithPinAlone'
  [never_send]='TestAuthS5_NewDeviceNeedsACodeToTheRegisteredPhone'
  [first_match]='TestAuthS11_SharedPhoneSignsNobodyIn|TestAuthS11_SharedMemberNumberSignsNobodyIn'
  [ambiguous_before_pin]='TestAuthS11_SharedPhoneWrongPinRefusesLikeAnyOther'
  [one_account_per_record]='TestAuthS11_Control_OnePersonTwoSocietiesIsOneAccount'
)

# SQL mutants (and M0) => the database tests each must turn red.
#
# M0 excludes, with the reason each is targeted elsewhere:
#   TestAuth0_ and the *Control* tests  pass on M0 by design (see CONTROL_RE)
#   TestAuthD1_   passes vacuously on M0 (nothing ever resets) → d_one_approval_enough
#   TestAuthG6_   BROKEN on M0 by design (no lock to release)  → g_release_ineffective
declare -gA TARGET_OF=(
  [M0]='TestAuthA1_|TestAuthA2_|TestAuthA3_|TestAuthB|TestAuthC[1-7]_|TestAuthD[2-7]_|TestAuthD_TwoOther|TestAuthE|TestAuthF[1-6]_|TestAuthF7_Resend|TestAuthF8_|TestAuthG[1-5]_|TestAuthG[7-9]_|TestAuthH[1-9]'
  # ---- batch 1 ----
  [clock_trusts_session]='TestAuth0_SessionCannotEnableTheTestClock'
  [a_any_phc_hash]='TestAuthA1_PinHashMustBeArgon2id'
  [a_rejects_everything]='TestAuthA_Control_HashesAccepted'
  [b_code_proof_counts]='TestAuthB1_'
  [b_any_owner]='TestAuthB2_ActorMustUseTheirOwnProof'
  [b_no_freshness]='TestAuthB3_StaleProofRefused'
  [b_reusable]='TestAuthB4_ProofIsSingleUse'
  [d_one_approval_enough]='TestAuthD1_OneApprovalDoesNotReset'
  # ---- batch 2: C officer reset · D two-signatory reset · E phone · F codes ----
  [c_self_by_user_only]='TestAuthC4_OfficerCannotResetThemselvesThroughOwnOtherRecord'
  [c_any_society]='TestAuthC5_OfficerOfAnotherSocietyRefused'
  [c_auditor_is_officer]='TestAuthC3_ResetByAuditorRefused'
  [c_reset_leaves_sessions]='TestAuthC7_ResetClearsPinAndEndsEverySession'
  [c_no_resets]='TestAuthC_Control_OfficerResetsMember'
  [d_target_may_approve]='TestAuthD2_TargetCannotApproveOwnReset'
  [d_no_distinct]='TestAuthD3_OnePersonCountsOnce'
  [d_any_officer]='TestAuthD4_ApproverMustBeASignatory'
  [d_ignores_term]='TestAuthD4_FormerSignatoryCannotApprove'
  [d_any_society]='TestAuthD4_SignatoryOfAnotherSocietyCannotApprove'
  [d_no_window]='TestAuthD5_ApprovalsWeeksApartDoNotCount'
  [d_proof_may_predate]='TestAuthD6_ApprovalCannotPredateTheRequest'
  [e_self_by_user_field]='TestAuthE2_MemberCannotChangeOwnPhone'
  [e_any_act_in_xact]='TestAuthE5_AppliedNumberMustBeTheRecordedNumber'
  [e_signatory_single_officer]='TestAuthE4_OneOfficerCannotChangeASignatorysPhone'
  [f_destination_first_record]='TestAuthF1_NoCodeWhenRecordsDisagreeOnPhone'
  [f_no_expiry]='TestAuthF4_CodeExpiresAfterFiveMinutes'
  [f_unbounded_attempts]='TestAuthF5_FourthAttemptRefusedEvenIfRight'
  [f_reusable_code]='TestAuthF6_CodeIsSingleUse'
  [f_no_resend_interval]='TestAuthF7_ResendNotWithinSixtySeconds'
  [f_resend_two_minutes]='TestAuthF7_Control_ResendAfterSixtySeconds'
  [f_never_accepts]='TestAuthF_Control_RightCodeInTime'
  # ---- batch 3: G lockout · H sessions ----
  [g_release_ineffective]='TestAuthG6_OfficerReleaseEndsTheLock'
  [g_per_device]='TestAuthG2_FailuresAcrossDevicesAccumulate'
  [g_success_resets_history]='TestAuthG4_SuccessDoesNotEraseLockoutHistory'
  [g_no_escalation]='TestAuthG3_SecondLockoutInADayIsThirtyMinutes'
  [g_no_officer_tier]='TestAuthG5_ThirdLockoutInADayNeedsAnOfficer'
  [g_window_forever]='TestAuthG8_LockoutsOlderThanADayDoNotEscalate'
  [g_release_self_allowed]='TestAuthG7_OfficerCannotReleaseOwnLock'
  [g_locks_at_four]='TestAuthG_Control_FourFailuresThenSuccess'
  [h_member_rule_for_all]='TestAuthH7_OfficerSessionLastsEightHours|TestAuthH8_ApproverSignedInAsMemberGetsEightHours'
  [h_officer_rule_for_all]='TestAuthH6_MemberSessionLastsThirtyDays'
  [h_privilege_at_start]='TestAuthH9_AppointedMidSessionShrinksToEightHours'
  [h_ended_term_reverts_to_member]='TestAuthH9_AppointmentEndingMidSessionEndsTheSession'
  [h_trusts_supplied_expiry]='TestAuthH10_SessionExpiryIsTheServers'
  [h_never_valid]='TestAuthH_Control_FreshSessionValid'
  [h_pin_change_ends_all]='TestAuthH1_PinChangeEndsOtherSessionsKeepsThisOne'
  [h_end_not_final]='TestAuthH2_EndedSessionCannotBeRevived'
  [h_end_keeps_device]='TestAuthH3_MemberEndsStolenDeviceFromElsewhere'
  [h_revocation_reversible]='TestAuthH4_RevokedDeviceCannotRevive'
  [h_anyone_can_end]='TestAuthH5_AnotherPersonCannotEndMySession'
)
