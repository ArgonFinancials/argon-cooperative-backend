# Suite vfdproxy — the VFD relay on the wallet's server (cmd/vfd-proxy,
# internal/vfdproxy; decided 2026-11-28), tested against a fake VFD inside the
# test: no network, no real token. Sourced by mutate.sh. The breaks are in the
# reference relay (proxyfake_test.go, GENERATED from internal/vfdproxy/proxy.go
# by genproxyfake.py), selected by ARGON_TEST_PROXY_FAKE. Run with --svc.
SUITE_INCLUDE='^TestVfdProxy'
SUITE_EXCLUDE='^$'
SUITE_SKIP=''
SUITE_RUN='^TestVfdProxy'
SVC_RUN='^TestVfdProxy'
SVC_ENV='ARGON_TEST_PROXY_FAKE'
SUITE_BASE='migrations/0001_init.up.sql,migrations/0002_tier1.up.sql'
SUITE_MUTDIR='mutants_vfdproxy_none'
SUITE_GREEN_NEEDS='internal/vfdproxy/proxy.go'      # the real relay, written after the tests
CONTROL_RE='^$'
declare -gA TARGET_OF=()
declare -gA SVC_TARGET_OF=(
  [signature_unchecked]='TestVfdProxy02_AnUnsignedOrWronglySignedCallIsRefusedAndVFDNeverAsked|TestVfdProxy03_TheSignatureCoversTheBody'
  [stale_ok]='TestVfdProxy04_AStaleOrReplayedCallIsRefused'
  [replay_ok]='TestVfdProxy04_AStaleOrReplayedCallIsRefused'
  [any_path]='TestVfdProxy05_OnlyTheCooperativesFiveCalls'
  [body_rewritten]='TestVfdProxy01_ASignedCallPassesOnceAndVFDsAnswerComesBackUnchanged'
  [status_rewritten]='TestVfdProxy01_ASignedCallPassesOnceAndVFDsAnswerComesBackUnchanged'
  [retried]='TestVfdProxy06_UnreachableAndSlowAreSaidApartAndNeverRetried'
  [timeout_as_unreachable]='TestVfdProxy06_UnreachableAndSlowAreSaidApartAndNeverRetried'
  [bvn_logged]='TestVfdProxy07_EveryCallIsRecordedNeverTheBVNNorTheToken'
  [token_logged]='TestVfdProxy07_EveryCallIsRecordedNeverTheBVNNorTheToken'
  [time_header_missing]='TestVfdProxy09_EveryAnswerCarriesTheRelaysClock'
  [unmarked_refusal]='TestVfdProxy02_AnUnsignedOrWronglySignedCallIsRefusedAndVFDNeverAsked'
  [rotation_off]='TestVfdProxy08_ThePreviousKeyIsAcceptedDuringARotation'
)
