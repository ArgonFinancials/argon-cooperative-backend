# Sabotaged copies of deploy/47-founding-officer.sh, one rule removed each,
# for test/ops/sabotage_founding_officer.sh: every test in
# founding_officer_test.sh must be made to fail by the copy that breaks what
# it checks. An edit that no longer matches the script stops the run.
import os, sys, shutil

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
OUT = sys.argv[1]
with open(os.path.join(REPO, "deploy", "47-founding-officer.sh"), encoding="utf-8") as f:
    src = f.read()

# name: (expected red tests, [(old, new), ...])
M = {
 "db_defaults": ({"O01"}, [("confirm=0 reissue=0 db=\n", "confirm=0 reissue=0 db=$ARGON_DB\n")]),
 "no_confirm_gate": ({"O02"}, [("((confirm)) || {", "true || {")]),
 "slip_printed_twice": ({"O03"}, [("ok \"issued ${v[name]}'s enrolment code", "echo \"        $slip\"\nok \"issued ${v[name]}'s enrolment code")]),
 "reprint_on_rerun": ({"O04"}, [("          info \"If the slip is lost, run again with --reissue: a new code, which voids that one, on your name and reason.\"\n          exit 4 ;;",
                                  "          info \"If the slip is lost, run again with --reissue: a new code, which voids that one, on your name and reason.\" ;;")]),
 "reissue_ignored": ({"O05"}, [("    --reissue) reissue=1; shift ;;", "    --reissue) shift ;;")]),
 "slip_before_database": ({"O06"}, [("read -r slip digest < <(draw) || true\n", "read -r slip digest < <(draw) || true\necho \"        $slip\"\n"),
                                     ("[[ $slip =~ ^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$ && $digest =~ ^[0-9a-f]{64}$ ]] ||", "[[ -n $slip || -z $slip ]] ||")]),
 "any_name_reused": ({"O07"}, [("  [[ $name == \"${v[name]}\" ]] ||", "  true ||")]),
 "key_as_argument": ({"O08"}, [('    AUTH_CODE_KEY="${ARGON_CODE_KEY:-}" "$FOUNDING_CODEGEN"', '    "$FOUNDING_CODEGEN" "${ARGON_CODE_KEY:-}"')]),
 "enrolled_reissued": ({"O09"}, [("    used) info \"${v[name]} already enrolled with a code ($at). A new code is needed only for a new device: run again with --reissue.\"\n          exit 4 ;;",
                                   "    used) ;;")]),
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
    with open(os.path.join(d, "47-founding-officer.sh"), "w", encoding="utf-8", newline="\n") as f:
        f.write(s)
    with open(os.path.join(d, "EXPECT"), "w", encoding="utf-8") as f:
        f.write(" ".join(sorted(exp)))
print(len(M), "mutants written")
