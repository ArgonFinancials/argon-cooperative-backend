#!/usr/bin/env bash
# THE SERVER. Applies an nginx change, and confirms it is TRUE, not reported:
#
#   apply.sh ADDR:PORT...     every address nginx must listen on afterwards
#
#   1. nginx -t: the config parses. It binds nothing, so it proves no listener.
#   2. a RESTART, never a reload. A reload keeps the old sockets open while it
#      opens the new ones, and Linux will not open 0.0.0.0:80 beside 85's
#      127.0.0.1:80: nginx logs "bind() ... failed", keeps the OLD config, and
#      systemd still says "Reloaded", because a reload is only a signal
#      (finding 66, the instance, 2026-11-06).
#   3. ss, until nginx holds each ADDR:PORT (5 s); otherwise it stops, naming
#      the address missing and quoting nginx's own last errors.
#
# A change is not applied until the thing changed is confirmed true.
# For the tests only: NGX_NGINX, NGX_SYSTEMCTL, NGX_ERROR_LOG.
# Tests: test/ops/going_live_test.sh P01-P02 (E01: the reload, reproduced).
set -euo pipefail
ngx=${NGX_NGINX:-nginx}; ctl=${NGX_SYSTEMCTL:-systemctl}; elog=${NGX_ERROR_LOG:-/var/log/nginx/error.log}
fail() { printf '\n\033[31mFATAL:\033[0m %s\n' "$*" >&2; exit 1; }
(($#)) || fail "apply.sh: name every address nginx must listen on afterwards"

"$ngx" -t 2>&1 | sed 's/^/    /' >&2
"$ngx" -t >/dev/null 2>&1 || fail "nginx configuration test failed"
"$ctl" restart nginx

held() { ss -Htlnp | awk -v a="$1" '$4 == a' | grep -q '("nginx",'; }
for a in "$@"; do
  for _ in $(seq 50); do held "$a" && continue 2; sleep 0.1; done
  { echo "    nginx's last errors ($elog):"; tail -5 "$elog" 2>/dev/null | sed 's/^/    /'; } >&2
  fail "nginx does not listen on $a after the restart"
done
printf '\033[32m ok\033[0m %s\n' "nginx listens on: $*" >&2
