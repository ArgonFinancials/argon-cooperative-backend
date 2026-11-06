# Sabotaged copies of deploy/46-create-society.sh, one rule removed each, for
# test/ops/sabotage_create_society.sh: every test in create_society_test.sh
# must be made to fail by the copy that breaks what it checks. An edit that no
# longer matches the script stops the run (a mutant that does not apply proves
# nothing).
import os, sys, shutil

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
OUT = sys.argv[1]
with open(os.path.join(REPO, "deploy", "46-create-society.sh"), encoding="utf-8") as f:
    src = f.read()

# name: (expected red tests, [(old, new), ...])
M = {
 # The gate, one rule at a time.
 "gate_any_store": ({"G01", "G05"}, [("[[ ${BACKUP_STORE:-} == s3://* ]] ||", "true ||")]),
 "gate_no_drill_ok": ({"G02"}, [('[[ -n $drill ]] || die "REFUSED: no passing drill', '[[ -n $drill ]] || exit 0; true || die "REFUSED: no passing drill')]),
 "gate_any_age": ({"G03", "G04"}, [("((age <= GATE_DAYS * 86400)) ||", "true ||")]),
 "gate_days_from_env": ({"G04"}, [("GATE_DAYS=8", "GATE_DAYS=${DRILL_MAX_AGE_D:-8}")]),
 "gate_after_database": ({"G05"}, [("# ---- the backup gate: before anything else, the database included ------------\n",
                                    "pg_as argon_migrator \"$db\" -At -c 'SELECT 1' </dev/null >/dev/null || die \"cannot reach the database '$db'\"\n")]),
 "gate_names_no_drill": ({"G06"}, [('ok "backups are off the instance, and the newest passing drill $((age / 86400))d old: $drill"', 'ok "backups are off the instance"')]),
 # What the script asks for, records and prints.
 "no_confirm_gate": ({"S01"}, [("((confirm)) || {", "true || {")]),
 "login_from_caller": ({"S02"}, [('    --login)   die "--login is refused', '    --login)   shift 2 ;;\n    --login-was) die "--login is refused')]),
 "value_optional": ({"S03"}, [('blank "${v[$f]-}" && die', 'false && die')]),
 "login_unrecorded": ({"S04"}, [('-v login="$login"', '-v login="operator"')]),
 "refusal_swallowed": ({"S05"}, [("  -f - <<'SQL'", "  -f - 2>/dev/null <<'SQL'")]),
 "db_defaults": ({"S07"}, [("confirm=0 db=\n", "confirm=0 db=$ARGON_DB\n")]),
 "words_mangled": ({"S06"}, [('-v op="${v[operator]}"', '-v op="${v[operator]//\\"/}"')]),
}

if os.path.isdir(OUT):
    shutil.rmtree(OUT)
for name, (exp, edits) in M.items():
    s = src
    for old, new in edits:
        n = s.count(old)
        if n != 1:
            sys.exit(f"MUTANT {name} DOES NOT APPLY: {n} matches for {old[:60]!r}")
        s = s.replace(old, new)
    d = os.path.join(OUT, name)
    os.makedirs(d)
    shutil.copy(os.path.join(REPO, "deploy", "lib.sh"), d)
    os.makedirs(os.path.join(d, "sql"))
    shutil.copy(os.path.join(REPO, "deploy", "sql", "grants.sql"), os.path.join(d, "sql"))
    with open(os.path.join(d, "46-create-society.sh"), "w", encoding="utf-8", newline="\n") as f:
        f.write(s)
    with open(os.path.join(d, "EXPECT"), "w", encoding="utf-8") as f:
        f.write(" ".join(sorted(exp)))
print(len(M), "mutants written")
