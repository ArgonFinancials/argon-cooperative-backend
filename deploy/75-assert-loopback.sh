#!/usr/bin/env bash
# THE SERVER. Asserts, by script rather than by anyone remembering, that every
# listening TCP socket on this host is on loopback except the ports named in
# PUBLIC_PORTS. Docker's published ports bypass ufw, so a container published
# on 0.0.0.0 is on the internet whatever the firewall says: this checks both
# the host's sockets and Docker's own view of its ports.
#
# The public ports are DERIVED, never set: 22 (sshd), and 22 80 443 once the
# API has a public name (ARGON_API_DOMAIN, 86-install-tls.sh). A PUBLIC_PORTS
# line in the env file is reported and not read: the example file shipped
# "PUBLIC_PORTS=22", and on the instance it overrode the public name, so the
# guard would have refused going live itself (finding 67). A public port is public
# for ITS program only: 22 for sshd and systemd (holding it for socket-
# activated ssh; either or both), 80 and 443 for nginx. Anything else on them,
# or a listener whose owner cannot be seen (run with sudo), is refused.
# Tests: test/ops/going_live_test.sh L01-L13.
#
# Run after every install step that can open a port; exit 1 names the offender.
source "$(dirname "$0")/lib.sh"
on_server || die "75-assert-loopback.sh is for the server (ARGON_HOST=1)"
[[ -n ${PUBLIC_PORTS+set} ]] && info "PUBLIC_PORTS (\"$PUBLIC_PORTS\") in $ARGON_ENV is not read: the public ports follow ARGON_API_DOMAIN. Delete the line."
if [[ -n ${ARGON_API_DOMAIN:-} ]]; then PUBLIC_PORTS="22 80 443"; else PUBLIC_PORTS="22"; fi

# The programs holding a socket, from ss's users:(("nginx",pid=…),…): "nginx ".
names() { grep -oE '\("[^"]+",' <<<"$1" | tr -d '(",' | sort -u | tr '\n' ' ' || true; }
# only NAMES ALLOWED...: someone holds it, and every holder is allowed. One
# socket can have several holders: with ssh.socket (Accept=no) systemd keeps 22
# AND hands it to the sshd listener, so ss names both (finding 64).
only() {
  local n=$1 w; shift
  [[ -n $n ]] || return 1
  for w in $n; do [[ " $* " == *" $w "* ]] || return 1; done
}
owner_ok() {  # owner_ok PORT PROC: only the program this port is public for holds it
  local n; n=$(names "$2")
  case $1 in
    22)     only "$n" sshd systemd ;;
    80|443) only "$n" nginx ;;
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
