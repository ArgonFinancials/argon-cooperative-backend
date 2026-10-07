# Suite 0010 — a signature and a rejection rest on the person's own PIN step-up
# (decided 2026-10-07). Sourced by mutate.sh. As a batch rests on its act
# (0005): own, the right kind, this transaction, once.
SUITE_INCLUDE='^TestSignStep'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestSignStep'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql,migrations/0003_auth.up.sql,migrations/0004_roles.up.sql,migrations/0005_step_up.up.sql,migrations/0006_line_society.up.sql,migrations/0007_approvals.up.sql,migrations/0008_loans.up.sql,migrations/0009_write_offs.up.sql,test/tier1/0010_interface.sql'
SUITE_MUTDIR='mutants_0010'
SUITE_GREEN_NEEDS='migrations/0010_sign_step_up.up.sql'   # absent until after review
CONTROL_RE='Control'
declare -gA SVC_TARGET_OF=()
declare -gA TARGET_OF=(
  [M0]='TestSignStep[0-9]'
  [m_signature_act_optional]='TestSignStep1_ASignatureRestsOnAStepUp'
  [m_signature_any_actor]='TestSignStep2_TheActIsTheSignersOwn'
  [m_signature_any_kind]='TestSignStep3_TheActIsASignatureAct'
  [m_signature_any_transaction]='TestSignStep4_TheActIsFromThisTransaction'
  [m_signature_act_reusable]='TestSignStep5_OneStepUpSignsOnce'
  [m_rejection_act_optional]='TestSignStep6_ARejectionRestsOnAStepUp'
  [m_rejection_any_actor]='TestSignStep7_TheActIsTheRejectersOwn'
  [m_rejection_any_kind]='TestSignStep8_TheActIsARejectionAct'
  [m_rejection_any_transaction]='TestSignStep9_TheRejectionActIsFromThisTransaction'
  [m_rejection_act_reusable]='TestSignStep10_OneStepUpRejectsOnce'
  [c_signature_act_must_be_earlier]='TestSignStep_Control_SigningWithTheirOwnStepUp'
  [c_rejection_needs_signature_act]='TestSignStep_Control_RejectingWithTheirOwnStepUp'
)
