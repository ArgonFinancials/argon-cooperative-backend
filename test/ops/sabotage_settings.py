# Sabotaged copies of deploy/45-society-settings.sh, one rule removed each, for
# test/ops/sabotage_settings.sh: every test in society_settings_test.sh must be
# made to fail by the copy that breaks what it checks. An edit that no longer
# matches the script stops the run (a mutant that does not apply proves nothing).
import os, sys, shutil

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
OUT = sys.argv[1]
with open(os.path.join(REPO, "deploy", "45-society-settings.sh"), encoding="utf-8") as f:
    src = f.read()

# name: (expected red tests, [(old, new), ...])
M = {
 "no_confirm_gate": ({"S02"}, [("if ((!confirm)); then\n  echo \"NOT APPLIED", "if false; then\n  echo \"NOT APPLIED")]),
 "preview_exits_0": ({"S02"}, [("make this change.\" >&2\n  exit 3", "make this change.\" >&2\n  exit 0")]),
 "login_from_caller": ({"S08"}, [("    --login)  die", "    --login)  login_in=$2; shift 2 ;;\n    --login-was) die"),
                                 ("[[ -n ${SUDO_USER:-} ]] && login+=", "[[ -n ${login_in:-} ]] && login=$login_in\n[[ -n ${SUDO_USER:-} ]] && login+=")]),
 "no_sudo_name": ({"S08"}, [("[[ -n ${SUDO_USER:-} ]] && login+=", "false && login+=")]),
 "sudo_claim_unlabelled": ({"S08"}, [(" [claimed: SUDO_USER from the environment, not verified]", "")]),
 "login_unlabelled": ({"S01", "S07", "S08", "S13"}, [(" [login, verified by id -un]", "")]),
 "operator_unchecked": ({"S03"}, [("blank \"$operator\" && die", "false && die")]),
 "reason_unchecked": ({"S04"}, [("blank \"$reason\"   && die", "false && die")]),
 "resolution_unchecked": ({"S05"}, [("blank \"$ref\"   && die", "false && die"), ("blank \"$rdate\" && die", "false && die"),
                                    ("[[ $rdate =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] ||", "true ||")]),
 "lifecycle_resolution_dropped": ({"S06"}, [("if ((have_ref || have_rdate)); then", "if ((have_ref || have_rdate)) && [[ $setting != lifecycle ]]; then")]),
 "series_resolution_dropped": ({"S15"}, [("if ((have_ref || have_rdate)); then", "if ((have_ref || have_rdate)) && [[ $setting != charge-series ]]; then")]),
 "refusal_unparsed": ({"S09", "S10", "S14"}, [("s/^\\(psql:[^ ]*: \\)\\{0,1\\}ERROR:", "s/^ERROR:")]),
 "refusal_reason_swallowed": ({"S09", "S10", "S14"}, [("printf '\\nREFUSED by the database%s:\\n  %s\\n' \"${rule:+ ($rule)}\" \"$msg\"", "printf '\\nREFUSED by the database%s.\\n' \"${rule:+ ($rule)}\"")]),
 "sql_spliced": ({"S17"}, [("q -v soc=\"$sid\" -v val=\"$value\" -v op=\"$operator\"", "q -v soc=\"$sid\" -v val=\"$value\" -v op=\"x\""),
                           ("sql=\"SELECT $fn(:'soc', :'val'::$cast, :'op',", "sql=\"SELECT $fn(:'soc', :'val'::$cast, '$operator',")]),
 "unknown_society_unchecked": ({"S18"}, [("[[ -n $sid ]] || die \"no society with code '$society' in '$db'\"", "")]),
 "value_unchecked": ({"S19"}, [("[[ $value =~ ^-?[0-9]+$ ]] || die", "true || die")]),
 "settings_not_listed": ({"S20"}, [("*) usage; die \"unknown setting '$setting'. The settings: ${SETTINGS// /, }\"", "*) die \"unknown setting '$setting'\"")]),
 "prints_password": ({"S22"}, [("echo \"APPLIED. The settings log now reads:\" >&2", "echo \"APPLIED. The settings log now reads:\" >&2\necho \"(as argon_migrator/$ARGON_MIGRATOR_PASSWORD)\" >&2")]),
 "naira_ungrouped": ({"S12"}, [("  while [[ $n =~", "  false && while [[ $n =~")]),
 "guard_unexplained": ({"S11"}, [("It is INTERFACE FRICTION, NOT AUTHORISATION: it decides what a person is asked to confirm, never who must sign. So it has NO MINIMUM; the quorum rule does not apply to it.", "")]),
 "show_without_log": ({"S21"}, [("FROM society_settings_log WHERE society_id = :'soc' ORDER BY at, id \\gx", "FROM society_settings_log WHERE false \\gx")]),
 "series_json_unchecked": ({"S16"}, [("' \"$file\" 2>&1) || die \"series file '$file': $parsed\"", "' \"$file\" 2>&1) || true"),
                                     ("[[ -f $file ]] || die \"series file '$file' not found\"", "")]),
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
    with open(os.path.join(d, "45-society-settings.sh"), "w", encoding="utf-8", newline="\n") as f:
        f.write(s)
    with open(os.path.join(d, "EXPECT"), "w", encoding="utf-8") as f:
        f.write(" ".join(sorted(exp)))
print(len(M), "mutants written")
