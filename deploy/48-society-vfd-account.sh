#!/usr/bin/env bash
# The operator records a society's VFD account: the account its members pay
# into and its payouts leave from (0028's operator_record_society_vfd_account;
# LEDGER_CONTRACT.md §12: "recorded by the operator from VFD's answer, never
# typed"). Tests: test/ops/society_vfd_account_test.sh.
#
#   48-society-vfd-account.sh --society CODE --account NNNNNNNNNN \
#       --operator "Your Name" --reason "why" --db NAME [--confirm]
#
# The operator gives the account NUMBER and nothing else about it. The name,
# VFD's client id and account id are VFD's own answer to a name enquiry (the
# API image's vfd-account-lookup), shown first and recorded as VFD gave them.
# An account VFD does not know, or a VFD that does not answer, records
# nothing. No --name: a name the operator types is not VFD's answer.
#
# --db is required, never defaulted (finding 69). Without --confirm it shows
# VFD's answer and does nothing (exit 3). A society has ONE VFD account: a
# second is the database's refusal, printed as written.
#
# VFD's credentials (VFD_BASE_URL, VFD_ACCESS_TOKEN, from the env file) reach
# the lookup in its environment, never on a command line. The BVN the
# enquiry carries is never printed, never stored.
source "$(dirname "$0")/lib.sh"
set +E

usage() { sed -n '7,8p' "$0" | sed 's/^# \{0,1\}//' >&2; }

declare -A v=()
flags="society account operator reason"
confirm=0 db=
while (($#)); do
  case $1 in
    --confirm) confirm=1; shift ;;
    --db)      db=${2-}; shift 2 ;;
    --login)   die "--login is refused: the script records the server login itself (user@host, and who ran sudo). A login the caller supplies is not a record of anything." ;;
    --name|--client|--client-id|--account-id)
               die "$1 is refused: the account's name and VFD's ids are VFD's answer to a name enquiry, never typed (LEDGER_CONTRACT §12). Give --account and nothing else about it." ;;
    -h|--help) usage; exit 1 ;;
    --*)       f=${1#--}; [[ " $flags " == *" $f "* ]] || { usage; die "unknown argument '$1'"; }
               v[$f]=${2-}; shift 2 ;;
    *)         usage; die "unknown argument '$1'" ;;
  esac
done

blank() { [[ -z ${1//[[:space:]]/} ]]; }
for f in $flags; do blank "${v[$f]-}" && die "--$f is required"; done
[[ -n $db ]] || die "--db is required: name the database (on the server, the one in ARGON_DB). The script never chooses one itself (finding 69)."
[[ ${v[account]} =~ ^[0-9]{10}$ ]] || die "--account '${v[account]}' is not ten digits: a VFD account number is"

login="$(id -un)@$(hostname) [login, verified by id -un]"
[[ -n ${SUDO_USER:-} ]] && login+="; sudo by $SUDO_USER [claimed: SUDO_USER from the environment, not verified]"

q() { pg_as argon_migrator "$db" -q -At -v ON_ERROR_STOP=1 "$@"; }

society=$(q -v code="${v[society]}" -f - <<'SQL'
SELECT id FROM societies WHERE code = :'code';
SQL
) || die "cannot read the database '$db'"
[[ -n $society ]] || die "no society with the code '${v[society]}' in $db"

held=$(q -v s="$society" -f - <<'SQL'
SELECT account_number || ' (' || coalesce(account_name, 'no name') || ', recorded ' || to_char(recorded_at, 'YYYY-MM-DD') || ')'
  FROM vfd_accounts WHERE society_id = :'s' AND member_id IS NULL;
SQL
) || die "cannot read ${v[society]}'s VFD account"
[[ -z $held ]] || die "${v[society]} already has its VFD account: $held. A society has one, and a recorded account is never edited: nothing done"

# VFD's answer, by the API image (or, in the tests, VFD_LOOKUP: a command
# taking the number and printing the same one JSON line).
lookup() {
  if [[ -n ${VFD_LOOKUP:-} ]]; then
    "$VFD_LOOKUP" "$1"
  else
    VFD_BASE_URL="${VFD_BASE_URL:-}" VFD_ACCESS_TOKEN="${VFD_ACCESS_TOKEN:-}" \
      docker run --rm -e VFD_BASE_URL -e VFD_ACCESS_TOKEN "$(api_image || echo no-running-api-container)" vfd-account-lookup "$1"
  fi
}
# `|| rc=$?`, never `; rc=$?`: under lib.sh's set -e, a lookup that exits 3
# or 4 would end the script right here, silently, before it says why.
rc=0; answer=$(lookup "${v[account]}" 2>"${TMPDIR:-/tmp}/vfd-lookup.$$.err") || rc=$?
err=$(cat "${TMPDIR:-/tmp}/vfd-lookup.$$.err" 2>/dev/null); rm -f "${TMPDIR:-/tmp}/vfd-lookup.$$.err"
py() { if python3 -c '' 2>/dev/null; then python3 "$@"; else python "$@"; fi; }
field() { py -c 'import json,sys; print(json.loads(sys.argv[1]).get(sys.argv[2],""))' "$answer" "$1" 2>/dev/null; }
case $rc in
  0) ;;
  3) die "VFD knows no account ${v[account]} (VFD's code: $(field code)). Nothing was recorded. Check the number against VFD's own onboarding letter." ;;
  4) die "VFD did not answer for ${v[account]} (outcome: $(field outcome); $(field code)). Nothing was recorded; try again later." ;;
  *) die "the lookup could not run (exit $rc): ${err:-no reason given}. Nothing was recorded. VFD's credentials must be in the env file (VFD_BASE_URL, VFD_ACCESS_TOKEN)." ;;
esac
name=$(field name) client=$(field clientId) acct=$(field accountId) number=$(field accountNo)
[[ $number == "${v[account]}" ]] || die "VFD answered for account '$number', not ${v[account]}: nothing was recorded"
blank "$name" && die "VFD's answer for ${v[account]} carries no account name: nothing was recorded"
blank "$client" && die "VFD's answer for ${v[account]} carries no client id: nothing was recorded"

cat >&2 <<EOF

A society's VFD account, as VFD answers for it
  society          ${v[society]}
  account          ${v[account]}
  name             $name   [VFD's answer]
  VFD client id    $client   [VFD's answer]
  VFD account id   ${acct:-none given}   [VFD's answer]
  by               ${v[operator]}  [$login]
  why              ${v[reason]}
EOF
((confirm)) || { info "nothing done: if this is the society's account, run again with --confirm"; exit 3; }

id=$(q -v s="$society" -v n="${v[account]}" -v c="$client" -v a="$acct" -v nm="$name" \
       -v op="${v[operator]}" -v login="$login" -v why="${v[reason]}" -f - <<'SQL'
SELECT operator_record_society_vfd_account(:'s', :'n', :'c', NULLIF(:'a', ''), :'nm', :'op', :'login', :'why');
SQL
) || die "the database refused the account (its reason is above): nothing was recorded"
ok "recorded ${v[society]}'s VFD account ${v[account]} ($name), as VFD answers for it"
