#!/usr/bin/env bash
# pg_dump -Fc of the argon database, plus a manifest describing what is in it,
# taken from THE SAME SNAPSHOT as the dump — so a restore can be checked
# against the manifest exactly, even while the API is writing.
#
# Uploads  dumps/YYYY/MM/DD/argon-<ts>[-label].{dump,manifest,sha256}
#
#   50-backup.sh [--label NAME] [--db NAME]
source "$(dirname "$0")/lib.sh"
require_env ARGON_BACKUP_PASSWORD

label=""; db=$ARGON_DB
while (($#)); do case $1 in
  --label) label="-$2"; shift 2 ;;
  --db)    db=$2; shift 2 ;;
  *) die "unknown arg $1" ;;
esac; done

ts=$(date -u +%Y%m%dT%H%M%SZ)
key="dumps/$(date -u +%Y/%m/%d)/argon-$ts$label"
work=$(mktemp -d); trap 'rm -rf "$work"; [[ -n ${cop_pid:-} ]] && kill "$cop_pid" 2>/dev/null || true' EXIT

# Hold a REPEATABLE READ transaction open, export its snapshot, compute the
# manifest inside it, and point pg_dump at the same snapshot.
info "snapshot"
coproc PSQL { pg_as argon_backup "$db" -q -At 2>&1; }
cop_pid=$PSQL_PID
exec {to}>&"${PSQL[1]}" {from}<&"${PSQL[0]}"
echo "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SELECT pg_export_snapshot();" >&$to
read -r -t 30 snap <&$from || die "could not export snapshot"
# A native Windows psql ends its lines with CR LF, and `read` keeps the CR.
snap=${snap%$'\r'}
[[ $snap =~ ^[0-9A-F]+-[0-9A-F]+-[0-9]+$ ]] || die "unexpected snapshot id: $snap"

printf '%s\n\\echo __END__\n' "$MANIFEST_SQL" >&$to
: > "$work/manifest"
while read -r -t 60 line <&$from; do
  line=${line%$'\r'}
  [[ $line == __END__ ]] && break
  [[ $line == ERROR:* ]] && die "manifest query failed: $line"
  echo "$line" >> "$work/manifest"
done
grep -q '^schema_version=' "$work/manifest" || die "manifest incomplete"
# A NULL anywhere in the manifest SQL yields a blank line — which would then
# "match" an equally blank line after restore and prove nothing.
! grep -qvE '^[a-z_]+[.=]' "$work/manifest" || die "manifest has malformed/blank lines: $(grep -nvE '^[a-z_]+[.=]' "$work/manifest" | head -3 | tr '\n' ' ')"
grep -qE '^rows\.ledger_lines=[0-9]+$' "$work/manifest" || die "manifest lacks a ledger_lines row count"

info "pg_dump (snapshot $snap)"
pg_tool argon_backup ARGON_BACKUP_PASSWORD pg_dump -d "$db" -Fc -Z 6 --snapshot="$snap" > "$work/dump"
echo "COMMIT;" >&$to; exec {to}>&-

[[ -s $work/dump ]] || die "empty dump"
(cd "$work" && sha256sum dump | awk '{print $1}') > "$work/sha256"
{ echo "# argon backup $ts db=$db snapshot=$snap"; cat "$work/manifest"; } > "$work/manifest.full"

info "upload to $BACKUP_STORE/$key.*"
store_put "$work/dump"          "$key.dump"
store_put "$work/manifest.full" "$key.manifest"
store_put "$work/sha256"        "$key.sha256"

# Read the object back and check it, rather than trusting the upload.
store_get "$key.dump" "$work/readback"
[[ "$(sha256sum "$work/readback" | awk '{print $1}')" == "$(cat "$work/sha256")" ]] \
  || die "read-back checksum mismatch for $key.dump"

ok "backup $key ($(du -h "$work/dump" | cut -f1), $(grep -c '^rows\.' "$work/manifest") tables, $(grep '^ledger_lines.sum_kobo' "$work/manifest"))"
echo "$key"
