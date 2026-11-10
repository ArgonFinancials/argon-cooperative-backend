#!/usr/bin/env bash
# Tests for deploy/47-founding-officer.sh, the operator's way of appointing a
# founding officer and issuing their enrolment code (0027, 0024;
# LEDGER_CONTRACT.md §12). Written before the script was run, 2026-11-10.
#
# The database enforces the rules (suites 0024, 0027). These tests are about
# what the SCRIPT adds:
#   O01 --db is required (finding 69); O02 nothing without --confirm;
#   O03 it appoints, issues, and prints the slip ONCE;
#   O04 a rerun with a live code never reprints and issues nothing (exit 4);
#   O05 --reissue issues a new code that voids the live one, and prints it;
#   O06 a code the image did not draw issues nothing and prints no slip;
#   O07 a member number that is someone else's is refused, nothing done;
#   O08 the key reaches the image in its environment, never its arguments,
#       and never appears in the output;
#   O09 an officer who has enrolled is told so; nothing issued without --reissue.
#
#   bash test/ops/founding_officer_test.sh [--keep]
#   OFFICER_SCRIPT=/path/to/copy   runs against another copy (sabotage runs)
#
# The image is a fake `codegen` (FOUNDING_CODEGEN) that prints a fresh slip
# and digest and records how it was called. Runs against a scratch
# argon_verify_officer_<ts>_<pid>, never a real database.
source "$(dirname "$0")/../../deploy/lib.sh"
set +e

SCRIPT="${OFFICER_SCRIPT:-$DEPLOY_DIR/47-founding-officer.sh}"
keep=0; [[ ${1:-} == --keep ]] && keep=1
W=$(mktemp -d)
db="argon_verify_officer_$(date -u +%Y%m%dt%H%M%S)_$$"
create_scratch_db "$db" >/dev/null || die "cannot create $db"
((keep)) || trap 'rm -rf "$W"; drop_scratch_db "$db" >/dev/null 2>&1 || true' EXIT
run_migrate "$db" up >/dev/null 2>&1 || die "migrations did not apply to $db"
apply_grants "$db" || die "grants did not apply to $db"
sql() { pg_as argon_migrator "$db" -At -q -c "$1" </dev/null; }
[[ $(sql "SELECT (to_regproc('operator_appoint_founding_officer') IS NOT NULL)::int") == 1 ]] ||
  die "schema before 0027: nothing for the script to call"

T=""; RED=(); N=0
red()  { echo "  RED  $T — $*"; RED+=("${T%% *}"); }
pass() { echo "  pass $T"; }

# ---- the fake image --------------------------------------------------------------
KEY=$(openssl rand -hex 32)
cat >"$W/codegen" <<EOF
#!/usr/bin/env bash
printf 'key=%s args=%s\n' "\${AUTH_CODE_KEY:+set}" "\$*" >>"$W/codegen.log"
[[ -f "$W/codegen.broken" ]] && { echo "Unable to find image 'argon-api:x'"; exit 125; }
c=\$(head -c 64 /dev/urandom | base32 | tr -dc '0-9A-HJKMNP-TV-Z' | head -c 16)
printf '%s-%s-%s-%s %s\n' "\${c:0:4}" "\${c:4:4}" "\${c:8:4}" "\${c:12:4}" "\$(head -c 32 /dev/urandom | sha256sum | cut -c1-64)"
EOF
chmod +x "$W/codegen"
{ cat "$ARGON_ENV"; printf 'ARGON_CODE_KEY=%s\nARGON_DB=argon_verify_no_such_db\n' "$KEY"; } >"$W/env"

S=FND$$
sql "SELECT operator_create_society('$S', 'Ikeja Teachers Cooperative', 'LSCS/2019/0412', 'Lagos', 2, 2, 2,
  'Adaeze Okafor', 'test@host', 'Founding resolution, item 3', '2026-09-30', 'test society')" >/dev/null || die "cannot found the test society"

OUT="$W/out"; RC=0
num() { M="F-$$-$((++N))"; }
run() {  # run [ARGS...] (after the defaults; a later flag wins)
  RC=0
  env -u SUDO_USER FOUNDING_CODEGEN="$W/codegen" ARGON_ENV="$W/env" bash "$SCRIPT" \
    --society "$S" --name "Ngozi Eze" --phone "+2348031234567" --member-number "$M" --joined-on 2019-03-01 \
    --post "${POST:-chairman}" --signatory yes --operator "Adaeze Okafor (Argon operations)" \
    --reason "Founding officers, on the founding resolution" --resolution "Founding resolution, item 4" \
    --resolution-date 2026-09-30 "$@" >"$OUT" 2>&1 </dev/null || RC=$?
}
slips() { grep -cE '^ +[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$' "$OUT"; }
codes() { sql "SELECT count(*) FROM enrolment_codes c JOIN members m ON m.user_id = c.user_id JOIN societies s ON s.id = m.society_id WHERE s.code = '$S' AND m.member_number = '$M'"; }
members() { sql "SELECT count(*) FROM members m JOIN societies s ON s.id = m.society_id WHERE s.code = '$S' AND m.member_number = '$M'"; }

T="O01 without --db it refuses, naming --db: it never chooses a database itself"
num; RC=0
env -u SUDO_USER FOUNDING_CODEGEN="$W/codegen" ARGON_ENV="$W/env" bash "$SCRIPT" --society "$S" --name "Ngozi Eze" --phone "+2348031234567" \
  --member-number "$M" --joined-on 2019-03-01 --post chairman --signatory yes --operator "A" --reason "r" --resolution "x" --resolution-date 2026-09-30 \
  --confirm >"$OUT" 2>&1 </dev/null || RC=$?
if ((RC == 0)); then red "accepted with no --db"
elif ! grep -q -- '--db is required' "$OUT"; then red "refused, but not for --db: $(tail -2 "$OUT")"
else pass; fi

T="O02 without --confirm it shows the officer and does nothing (exit 3)"
num; POST=chairman run --db "$db"
if ((RC != 3)) || [[ $(members) != 0 || $(codes) != 0 ]]; then red "rc $RC, members $(members), codes $(codes)"
elif ! grep -q "$M" "$OUT"; then red "showed nothing of what it would do"
else pass; fi

T="O03 it appoints the officer, issues a code, and prints the slip once"
POST=chairman run --db "$db" --confirm
if ((RC != 0)) || [[ $(members) != 1 || $(codes) != 1 ]]; then red "rc $RC, members $(members), codes $(codes): $(tail -3 "$OUT")"
elif [[ $(slips) != 1 ]]; then red "the slip was printed $(slips) times: $(cat "$OUT")"
else pass; fi
FIRST=$(grep -oE '[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}' "$OUT" | head -1)

T="O04 a rerun with a live code never reprints it and issues nothing (exit 4)"
POST=chairman run --db "$db" --confirm
if ((RC != 4)) || [[ $(codes) != 1 ]]; then red "rc $RC, codes $(codes)"
elif [[ $(slips) != 0 ]] || grep -q "$FIRST" "$OUT"; then red "a slip was printed on a rerun: $(cat "$OUT")"
elif ! grep -q 'already has a live enrolment code' "$OUT" || ! grep -q -- '--reissue' "$OUT"; then red "did not say a code exists and how to reissue: $(tail -3 "$OUT")"
else pass; fi

T="O05 --reissue issues a new code, voiding the live one, and prints the new slip"
POST=chairman run --db "$db" --confirm --reissue
voided=$(sql "SELECT count(*) FROM enrolment_codes c JOIN members m ON m.user_id = c.user_id WHERE m.member_number = '$M' AND c.void_reason = 'reissued'")
if ((RC != 0)) || [[ $(codes) != 2 || $voided != 1 ]]; then red "rc $RC, codes $(codes), voided $voided: $(tail -3 "$OUT")"
elif [[ $(slips) != 1 ]] || grep -q "$FIRST" "$OUT"; then red "the new slip was not printed once, or the old one was: $(cat "$OUT")"
else pass; fi

T="O06 a code the image did not draw issues nothing and prints no slip"
num; touch "$W/codegen.broken"
POST=treasurer run --db "$db" --confirm
rm -f "$W/codegen.broken"
if ((RC == 0)) || [[ $(codes) != 0 ]]; then red "rc $RC, codes $(codes)"
elif [[ $(slips) != 0 ]]; then red "a slip was printed: $(cat "$OUT")"
elif ! grep -q 'did not draw a code' "$OUT"; then red "refused, but not saying why: $(tail -2 "$OUT")"
else pass; fi

T="O07 a member number that is someone else's is refused, nothing done"
RC=0
env -u SUDO_USER FOUNDING_CODEGEN="$W/codegen" ARGON_ENV="$W/env" bash "$SCRIPT" --society "$S" --name "Somebody Else" --phone "+2348030000000" \
  --member-number "$M" --joined-on 2019-03-01 --post secretary --signatory no --operator "A" --reason "r" --resolution "x" --resolution-date 2026-09-30 \
  --db "$db" --confirm --reissue >"$OUT" 2>&1 </dev/null || RC=$?
if ((RC == 0)) || [[ $(slips) != 0 ]]; then red "rc $RC: $(tail -3 "$OUT")"
elif ! grep -q 'is Ngozi Eze, not Somebody Else' "$OUT"; then red "refused, but not for the name: $(tail -2 "$OUT")"
else pass; fi

T="O08 the key reaches the image in its environment, never its arguments, never the output"
if ! grep -q '^key=set ' "$W/codegen.log"; then red "the image was called without the key in its environment: $(cat "$W/codegen.log")"
elif grep -q "$KEY" "$W/codegen.log"; then red "the key was in the image's arguments"
elif grep -rq "$KEY" "$W/out"; then red "the key was printed"
else pass; fi

T="O09 an officer who has enrolled is told so; nothing issued without --reissue"
num; POST=secretary run --db "$db" --confirm
uid=$(sql "SELECT user_id FROM members WHERE member_number = '$M'")
sql "SELECT enrolment_redeem('$uid', code_digest, repeat('e', 64)) FROM enrolment_codes WHERE user_id = '$uid'" >/dev/null
POST=secretary run --db "$db" --confirm
if ((RC != 4)) || [[ $(codes) != 1 ]]; then red "rc $RC, codes $(codes): $(tail -3 "$OUT")"
elif [[ $(slips) != 0 ]]; then red "a slip was printed: $(cat "$OUT")"
elif ! grep -q 'already enrolled' "$OUT"; then red "did not say they enrolled: $(tail -2 "$OUT")"
else pass; fi

echo
if ((${#RED[@]})); then echo "RED: ${RED[*]}"; exit 1; fi
echo "all founding-officer tests pass"
