#!/usr/bin/env bash
# THE SERVER. Asserts, by script rather than by anyone remembering, that every
# listening TCP socket on this host is on loopback except the ports named in
# PUBLIC_PORTS (default: 22, sshd). Docker's published ports bypass ufw, so a
# container published on 0.0.0.0 is on the internet whatever the firewall says:
# this checks both the host's sockets and Docker's own view of its ports.
#
# Run after every install step that can open a port; exit 1 names the offender.
source "$(dirname "$0")/lib.sh"
on_server || die "75-assert-loopback.sh is for the server (ARGON_HOST=1)"
: "${PUBLIC_PORTS:=22}"

bad=0
while read -r local proc; do
  port=${local##*:}; addr=${local%:*}
  [[ $addr =~ ^127\. || $addr == '[::1]' ]] && continue
  [[ " $PUBLIC_PORTS " == *" $port "* ]] && continue
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
