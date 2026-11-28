#!/usr/bin/env bash
# THE SERVER. A changed phone number's OLD phone, told or not (0045,
# 2026-11-28). A number is how a member signs in, so changing it takes over
# the account; the one person who would notice holds the old phone. Every
# change writes a notice to the old number, and the API sends it after the
# change commits. Two things need a person:
#
#   - a notice still WAITING 15 minutes on: the API stopped between the change
#     and the send, and the old phone was never told;
#   - an old phone NOT TOLD in the last day (refused, not reached, unknown, or
#     no SMS sender): exactly the case where someone should look twice.
#
#   68-check-phone-notices.sh --db NAME
#
# Nothing to say: exit 0. Otherwise one alert listing each (alert.sh), exit 3.
# The notices cannot be read: an alert saying so, exit 3; never "all told".
# 65-check-freshness.sh runs it on its timer. PHONE_NOTICES_ALERT (the tests')
# is a command given the alert's title and its text on stdin. Numbers are
# shown by their last four digits only.
source "$(dirname "$0")/lib.sh"
set +E

db=
while (($#)); do
  case $1 in
    --db) db=${2-}; shift 2 ;;
    *) die "unknown argument '$1' (68-check-phone-notices.sh --db NAME)" ;;
  esac
done
[[ -n $db ]] || die "--db is required: name the database (on the server, the one in ARGON_DB). The script never chooses one itself (finding 69)."

alert() {  # alert TITLE: the text on stdin
  if [[ -n ${PHONE_NOTICES_ALERT:-} ]]; then "$PHONE_NOTICES_ALERT" "$1"
  else bash "$DEPLOY_DIR/alert.sh" raise "$1" -; fi
}

has=$(pg_as argon_app "$db" -At -c "SELECT (to_regclass('public.phone_change_notices') IS NOT NULL)::int" </dev/null 2>/dev/null) || has=error
case $has in
  0) ok "no number change is told: this database is before 0045"; exit 0 ;;
  1) ;;
  *) echo "The notices to changed numbers' old phones could not be read from $db." | alert "old phones told: cannot read"; exit 3 ;;
esac

rc=0
out=$(pg_as argon_app "$db" -At -F ' | ' -c "SELECT so.registered_name, m.member_number, '...' || right(n.old_phone, 4),
      coalesce(s.outcome, 'waiting'), to_char(n.at AT TIME ZONE 'Africa/Lagos', 'YYYY-MM-DD HH24:MI')
    FROM phone_change_notices n JOIN members m ON m.id = n.member_id JOIN societies so ON so.id = m.society_id
    LEFT JOIN phone_change_notice_sends s ON s.notice_id = n.id
    WHERE (s.id IS NULL AND n.at < now() - interval '15 minutes')
       OR (s.outcome <> 'accepted' AND s.at > now() - interval '26 hours')
    ORDER BY n.at" </dev/null 2>&1) || rc=$?
if ((rc != 0)); then
  { echo "The notices to changed numbers' old phones could not be read: nobody can say every old phone was told."; echo; echo "$out"; } | alert "old phones told: cannot read"
  exit 3
fi
mapfile -t untold < <(grep -v '^$' <<<"$out")
if ((${#untold[@]} == 0)); then
  ok "every changed number's old phone was told"
  exit 0
fi
printf '%s\n' "${untold[@]}" | sed 's/^/  !! /' >&2
{ echo "A member's phone number was changed and the OLD phone was not told. The number is how they sign in: whoever holds the old phone is the one who would notice a takeover."
  echo "Call the society: confirm with the member, in person, that they asked for the change."
  echo; echo "society | member | old number | what came of the SMS | changed at (WAT)"
  printf -- '- %s\n' "${untold[@]}"
} | alert "old phone not told of a number change (${#untold[@]})"
exit 3
