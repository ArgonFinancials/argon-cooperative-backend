#!/usr/bin/env bash
# THE SERVER. Asserts, by script rather than by anyone remembering, that every
# listening TCP socket on this host is on loopback except the ports named in
# PUBLIC_PORTS. Docker's published ports bypass ufw, so a container published
# on 0.0.0.0 is on the internet whatever the firewall says: this checks both
# the host's sockets and Docker's own view of its ports.
#
# PUBLIC_PORTS defaults to 22 (sshd), and to 22 80 443 once the API has a
# public name (ARGON_API_DOMAIN, 86-install-tls.sh). A public port is public
# for ITS program only: 22 for sshd (or systemd, holding it for socket-
# activated ssh), 80 and 443 for nginx. Anything else on them, or a listener
# whose owner cannot be seen (run with sudo), is refused.
# Tests: test/ops/going_live_test.sh L01-L07.
#
# Run after every install step that can open a port; exit 1 names the offender.
source "$(dirname "$0")/lib.sh"
on_server || die "75-assert-loopback.sh is for the server (ARGON_HOST=1)"
if [[ -n ${ARGON_API_DOMAIN:-} ]]; then : "${PUBLIC_PORTS:=22 80 443}"; else : "${PUBLIC_PORTS:=22}"; fi

# The programs holding a socket, from ss's users:(("nginx",pid=…),…): "nginx ".
names() { grep -oE '\("[^"]+",' <<<"$1" | tr -d '(",' | sort -u | tr '\n' ' ' || true; }
owner_ok() {  # owner_ok PORT PROC: only the program this port is public for holds it
  local n; n=$(names "$2")
  case $1 in
    22)     [[ $n == "sshd " || $n == "systemd " ]] ;;
    80|443) [[ $n == "nginx " ]] ;;
    *)      [[ -n $n ]] ;;
  esac
}

bad=0
while read -r local proc; do
  port=${local##*:}; addr=${local%:*}
  [[ $addr =~ ^127\. || $addr == '[::1]' ]] && continue
  if [[ " $PUBLIC_PORTS " == *" $port "* ]]; then
    owner_ok "$port" "${proc:-}" && continue
    printf '\033[31m  !! port %s is public, but not for this: %s\033[0m\n' "$port" "${proc:-owner not visible: run with sudo}" >&2
    bad=1; continue
  fi
  printf '\033[31m  !! listening beyond loopback: %s %s\033[0m\n' "$local" "$proc" >&2
  bad=1
done < <(ss -Htlnp | awk '{print $4, $6}')

if command -v docker >/dev/null; then
  while read -r name ports; do
    [[ -z $ports ]] && continue
    if grep -qE '(^|, )(0\.0\.0\.0|\[::\]|::):' <<<"$ports"; then
      printf '\033[31m  !! container %s published beyond loopback: %s\033[0m\n' "$name" "$ports" >&2
      bad=1
    fi
  done < <(docker ps --format '{{.Names}} {{.Ports}}')
fi

((bad == 0)) || die "something listens beyond loopback (allowed: ports $PUBLIC_PORTS)"
ok "every TCP listener and every published container port is on loopback, except ports: $PUBLIC_PORTS"
