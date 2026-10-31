# Sabotaged copies of deploy/ for test/ops/sabotage_going_live.sh, one rule
# removed each: every test in going_live_test.sh must be made to fail by the
# copy that breaks what it checks. An edit that no longer matches its file
# stops the run (a mutant that does not apply proves nothing).
import os, sys, shutil

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
OUT = sys.argv[1]

CO = "nginx/check-origin.sh"
RP = "nginx/render-public.sh"
LB = "75-assert-loopback.sh"
AC = "nginx/argon-acme.conf.in"
TL = "nginx/argon-api-tls.conf.in"

# name: (expected red tests, [(file, old, new), ...])
M = {
 # Only the first of a list checked: the comma rule alone is not the guard
 # (the host pattern refuses a comma too), so the copy checks the first origin.
 "origin_two_ok":      ({"O02"}, [(CO, '[[ $origin != *,* ]] ||', 'origin=${origin%%,*}; true ||')]),
 "origin_http_ok":     ({"O03"}, [(CO, '[[ $origin =~ ^https://(', '[[ $origin =~ ^https?://(')]),
 "origin_any_domain":  ({"O04", "O05"}, [(CO, '[[ $host == "$parent" || $host == *".$parent" ]] ||', 'true ||')]),
 "origin_suffix_nodot": ({"O05"}, [(CO, '$host == *".$parent"', '$host == *"$parent"')]),
 "origin_path_ok":     ({"O06"}, [(CO, ')+)$ ]] ||', ')+)([:/].*)?$ ]] ||')]),
 "origin_empty_ok":    ({"O07"}, [(CO, '[[ -n $origin ]] || fail', '[[ -n $origin ]] || exit 0; true || fail')]),
 "render_hole_ok":     ({"R01"}, [(RP, "if grep -qE '@[A-Z_]+@' <<<\"$out\"; then", "if false; then")]),
 "render_empty_ok":    ({"R02"}, [(RP, '[[ -n ${!v:-} ]] || {', 'true || {')]),
 "loopback_80_always": ({"L01"}, [(LB, 'else : "${PUBLIC_PORTS:=22}"; fi', 'else : "${PUBLIC_PORTS:=22 80 443}"; fi')]),
 "loopback_any_owner": ({"L03", "L05", "L06"}, [(LB, '  local n; n=$(names "$2")', '  return 0')]),
 "loopback_unseen_ok": ({"L06"}, [(LB, '  local n; n=$(names "$2")', '  [[ -z $2 ]] && return 0\n  local n; n=$(names "$2")')]),
 "loopback_8080_public": ({"L04"}, [(LB, '=22 80 443}"; else', '=22 80 443 8080}"; else')]),
 "loopback_sshd_only": ({"L07"}, [(LB, '[[ $n == "sshd " || $n == "systemd " ]]', '[[ $n == "sshd " ]]')]),
 "acme_no_challenge":  ({"N01"}, [(AC, 'location ^~ /.well-known/acme-challenge/ {', 'location ^~ /.well-known/not-the-challenge/ {')]),
 "http_proxies_post":  ({"N03"}, [(AC, "return 403 '{\"code\":\"tls_required\",\"message\":\"This API is served over HTTPS only.\"}';", "proxy_pass http://@UPSTREAM@;")]),
 "http_no_default":    ({"N04"}, [(AC, "    return 444;\n", "")]),
 "no_hsts":            ({"N05"}, [(TL, '    # A year. No includeSubDomains: this header binds this name only.\n    add_header Strict-Transport-Security "max-age=31536000" always;\n', "")]),
 "short_hsts":         ({"N05"}, [(TL, '    # A year. No includeSubDomains: this header binds this name only.\n    add_header Strict-Transport-Security "max-age=31536000" always;', '    add_header Strict-Transport-Security "max-age=300" always;')]),
 "tls_default_serves": ({"N06"}, [(TL, "    ssl_reject_handshake on;", "    ssl_certificate @CERT@;\n    ssl_certificate_key @KEY@;")]),
 "preflight_counted":  ({"N07"}, [(TL, '    OPTIONS "";\n', '')]),
 "signin_unlimited":   ({"N08"}, [(TL, "location = /auth/sign-in {\n        limit_req zone=argon_auth burst=30 nodelay;\n", "location = /auth/sign-in {\n")]),
 "verify_unlimited":   ({"N11"}, [(TL, "location = /auth/codes/verify {\n        limit_req zone=argon_auth burst=30 nodelay;\n", "location = /auth/codes/verify {\n")]),
 "limit_everything":   ({"N12"}, [(TL, "    location / {\n        proxy_pass", "    location / {\n        limit_req zone=argon_auth burst=30 nodelay;\n        proxy_pass")]),
 "429_no_cors":        ({"N09"}, [(TL,"        add_header Access-Control-Allow-Origin $argon_cors_origin always;\n        add_header Access-Control-Allow-Credentials $argon_cors_credentials always;\n        add_header Vary Origin always;\n        add_header Retry-After", "        add_header Retry-After")]),
 "cors_any_origin":    ({"N10"}, [(TL, 'map $http_origin $argon_cors_origin {\n    "@APP_ORIGIN@" $http_origin;\n    default "";', 'map $http_origin $argon_cors_origin {\n    "@APP_ORIGIN@" $http_origin;\n    default $http_origin;')]),
 "no_body_limit":      ({"N13"}, [(TL, "client_max_body_size 1m;", "client_max_body_size 0;")]),
 "no_api_down_page":   ({"N14"}, [(TL, "    error_page 502 503 504 = @api_down;\n", "")]),
}

src = os.path.join(REPO, "deploy")
for name, (expect, edits) in M.items():
    d = os.path.join(OUT, name)
    # Never the env directory: it may hold a real env file.
    shutil.copytree(src, os.path.join(d, "deploy"), ignore=shutil.ignore_patterns("env"))
    for f, old, new in edits:
        p = os.path.join(d, "deploy", f)
        with open(p, encoding="utf-8", newline="") as fh:
            s = fh.read()
        if s.count(old) != 1:
            sys.exit(f"{name}: edit does not match exactly once in {f}: {old[:60]!r}")
        with open(p, "w", encoding="utf-8", newline="") as fh:
            fh.write(s.replace(old, new))
    with open(os.path.join(d, "EXPECT"), "w", encoding="utf-8") as fh:
        fh.write(" ".join(sorted(expect)))
print(f"{len(M)} sabotaged copies")
