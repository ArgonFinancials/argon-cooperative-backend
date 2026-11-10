#!/usr/bin/env bash
# The operator appoints a founding officer on the founding resolution, issues
# their enrolment code, and prints the slip ONCE. (0027's
# operator_appoint_founding_officer, 0024's operator_issue_enrolment;
# LEDGER_CONTRACT.md §12. Tests: test/ops/founding_officer_test.sh.)
#
#   47-founding-officer.sh --society CODE --name "Full name" --phone +234... \
#       --member-number N --joined-on YYYY-MM-DD --post chairman --signatory yes|no \
#       --operator "Your Name" --reason "why" \
#       --resolution "minutes reference" --resolution-date YYYY-MM-DD --db NAME [--reissue] [--confirm]
#
# --db is required, never defaulted (finding 69). Without --confirm it shows
# what it would do and does nothing (exit 3).
#
# ONCE MEANS ONCE. The server keeps only a code's digest, so a slip can never
# be printed again by anyone. Run again for an officer who already has a live
# code, or who has already enrolled, and it says so and stops (exit 4): it
# never reprints and never issues silently. --reissue issues a NEW code,
# which voids the live one (the database records which replaced which), on
# the operator's name and reason. An operator who could reprint a slip could
# take an officer's account; one who reissues does it on the record.
#
# The code is drawn by the API image itself (its enrolment-code command), with
# the server's AUTH_CODE_KEY passed in its environment, never its arguments:
# one implementation of the code, and the key in no command line or log. The
# slip is printed only after the database has accepted its digest.
source "$(dirname "$0")/lib.sh"
set +E

usage() { sed -n '7,10p' "$0" | sed 's/^# \{0,1\}//' >&2; }

declare -A v=()
flags="society name phone member-number joined-on post signatory operator reason resolution resolution-date"
confirm=0 reissue=0 db=
while (($#)); do
  case $1 in
    --confirm) confirm=1; shift ;;
    --reissue) reissue=1; shift ;;
    --db)      db=${2-}; shift 2 ;;
    --login)   die "--login is refused: the script records the server login itself (user@host, and who ran sudo). A login the caller supplies is not a record of anything." ;;
    -h|--help) usage; exit 1 ;;
    --*)       f=${1#--}; [[ " $flags " == *" $f "* ]] || { usage; die "unknown argument '$1'"; }
               v[$f]=${2-}; shift 2 ;;
    *)         usage; die "unknown argument '$1'" ;;
  esac
done

blank() { [[ -z ${1//[[:space:]]/} ]]; }
for f in $flags; do blank "${v[$f]-}" && die "--$f is required"; done
[[ -n $db ]] || die "--db is required: name the database (on the server, the one in ARGON_DB). The script never chooses one itself (finding 69)."
for f in joined-on resolution-date; do
  [[ ${v[$f]} =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || die "--$f '${v[$f]}' is not YYYY-MM-DD"
done
case ${v[signatory]} in yes) signs=true ;; no) signs=false ;; *) die "--signatory is yes or no, not '${v[signatory]}'" ;; esac

login="$(id -un)@$(hostname) [login, verified by id -un]"
[[ -n ${SUDO_USER:-} ]] && login+="; sudo by $SUDO_USER [claimed: SUDO_USER from the environment, not verified]"

q() { pg_as argon_migrator "$db" -q -At -v ON_ERROR_STOP=1 "$@"; }

society=$(q -v code="${v[society]}" -f - <<'SQL'
SELECT id FROM societies WHERE code = :'code';
SQL
) || die "cannot read the database '$db'"
[[ -n $society ]] || die "no society with the code '${v[society]}' in $db"

# The officer: appointed already (a rerun), or to be appointed now.
existing=$(q -v s="$society" -v n="${v[member-number]}" -f - <<'SQL'
SELECT m.id || '|' || m.user_id || '|' || u.full_name FROM members m JOIN users u ON u.id = m.user_id
 WHERE m.society_id = :'s' AND m.member_number = :'n';
SQL
)
member= user=
if [[ -n $existing ]]; then
  IFS='|' read -r member user name <<<"$existing"
  [[ $name == "${v[name]}" ]] ||
    die "member number ${v[member-number]} in ${v[society]} is $name, not ${v[name]}: nothing done"
  info "${v[name]} (${v[member-number]}) is already appointed in ${v[society]}"
fi

cat >&2 <<EOF

A founding officer
  society            ${v[society]}
  officer            ${v[name]}, ${v[phone]}, member ${v[member-number]} (joined ${v[joined-on]})
  post               ${v[post]}, signs: ${v[signatory]}
  on the resolution  ${v[resolution]}, ${v[resolution-date]}
  by                 ${v[operator]}  [$login]
  why                ${v[reason]}
EOF
((confirm)) || { info "nothing done: run again with --confirm"; exit 3; }

if [[ -z $member ]]; then
  member=$(q -v s="$society" -v name="${v[name]}" -v phone="${v[phone]}" -v num="${v[member-number]}" -v joined="${v[joined-on]}" \
    -v post="${v[post]}" -v signs="$signs" -v op="${v[operator]}" -v login="$login" -v ref="${v[resolution]}" \
    -v rdate="${v[resolution-date]}" -v why="${v[reason]}" -f - <<'SQL'
SELECT operator_appoint_founding_officer(:'s', :'name', :'phone', :'num', :'joined'::date, :'post'::officer_post, :'signs'::boolean,
                                         :'op', :'login', :'ref', :'rdate'::date, :'why');
SQL
  ) || die "the database refused the appointment (its reason is above): nothing was done"
  ok "appointed ${v[name]} as ${v[post]} of ${v[society]}"
fi

# Once means once: a live code, or an enrolment already made, stops here.
state=$(q -v m="$member" -f - <<'SQL'
SELECT coalesce((SELECT 'live|' || to_char(issued_at, 'YYYY-MM-DD HH24:MI') || '|' || to_char(expires_at, 'YYYY-MM-DD')
                   FROM enrolment_codes c JOIN members m ON m.user_id = c.user_id
                  WHERE m.id = :'m' AND c.used_at IS NULL AND c.voided_at IS NULL),
                (SELECT 'used|' || to_char(max(used_at), 'YYYY-MM-DD HH24:MI') || '|'
                   FROM enrolment_codes c JOIN members m ON m.user_id = c.user_id
                  WHERE m.id = :'m' AND c.used_at IS NOT NULL HAVING count(*) > 0), 'none||');
SQL
) || die "cannot read ${v[name]}'s enrolment codes"
IFS='|' read -r kind at until <<<"$state"
if ((!reissue)); then
  case $kind in
    live) info "${v[name]} already has a live enrolment code (issued $at, good until $until). It is NOT shown again: the server keeps only its digest."
          info "If the slip is lost, run again with --reissue: a new code, which voids that one, on your name and reason."
          exit 4 ;;
    used) info "${v[name]} already enrolled with a code ($at). A new code is needed only for a new device: run again with --reissue."
          exit 4 ;;
  esac
fi

# Drawn by the API image; the key in its environment, never an argument.
draw() {
  if [[ -n ${FOUNDING_CODEGEN:-} ]]; then
    AUTH_CODE_KEY="${ARGON_CODE_KEY:-}" "$FOUNDING_CODEGEN"
  else
    AUTH_CODE_KEY="${ARGON_CODE_KEY:-}" docker run --rm -e AUTH_CODE_KEY "argon-api:$(cat "$REPO_DIR/RELEASE")" enrolment-code
  fi
}
read -r slip digest < <(draw) || true
[[ $slip =~ ^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$ && $digest =~ ^[0-9a-f]{64}$ ]] ||
  die "the API image did not draw a code (is argon-api built, and ARGON_CODE_KEY set?): nothing was issued"

q -v m="$member" -v d="$digest" -v op="${v[operator]}" -v login="$login" -v why="${v[reason]}" -f - >/dev/null <<'SQL' ||
SELECT operator_issue_enrolment(:'m', :'d', :'op', :'login', :'why');
SQL
  die "the database refused the code (its reason is above): nothing was issued, and no slip is shown"
unset digest

cat <<EOF

  ┌──────────────────────────────────────────────────────────────┐
    Enrolment slip for ${v[name]} (${v[society]}, ${v[member-number]})

        $slip

    Good for 30 days, once. Write it down now: it is never shown again.
    Hand it over in person. They sign in with their member number,
    this code, and the PIN they choose.
  └──────────────────────────────────────────────────────────────┘

EOF
ok "issued ${v[name]}'s enrolment code$( ((reissue)) && [[ $kind == live ]] && echo ", voiding the one issued $at")"
