#!/usr/bin/env bash
# THE SERVER. The CI deploy user: a dedicated account whose key can run ONE
# command, as root, and nothing else.
#
#   sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/25-deploy-user.sh /path/to/argon-deploy.pub
#
#   - user argon-deploy: no password, no other sudo, not in the docker group
#   - authorized_keys: restrict,command="sudo /opt/argon/bin/argon-deploy"
#     (no shell, no port forwarding, no agent or X11, no pty; the release
#     arrives on stdin)
#   - sudoers: exactly /opt/argon/bin/argon-deploy, no arguments
#
# Never the key that opens other hosts (prod-loan-wallet.pem): a key of its own,
# whose private half lives only in the CI secret store.
source "$(dirname "$0")/lib.sh"
on_server || die "25-deploy-user.sh is for the server (ARGON_HOST=1)"
[[ $EUID -eq 0 ]] || die "run with sudo"
pub=${1:-}
[[ -f $pub ]] || die "usage: 25-deploy-user.sh /path/to/argon-deploy.pub"
grep -qE '^ssh-ed25519 [A-Za-z0-9+/=]+( .*)?$' "$pub" || die "$pub is not a single ed25519 public key"
[[ $(wc -l < "$pub") -le 1 ]] || die "$pub holds more than one key"

install -m 0755 "$DEPLOY_DIR/argon-deploy" /opt/argon/bin/argon-deploy
id argon-deploy &>/dev/null || useradd --create-home --shell /bin/bash argon-deploy
passwd -l argon-deploy >/dev/null
install -d -m 0700 -o argon-deploy -g argon-deploy /home/argon-deploy/.ssh
printf 'restrict,command="sudo /opt/argon/bin/argon-deploy" %s\n' "$(cat "$pub")" > /home/argon-deploy/.ssh/authorized_keys
chown argon-deploy:argon-deploy /home/argon-deploy/.ssh/authorized_keys
chmod 0600 /home/argon-deploy/.ssh/authorized_keys

printf 'argon-deploy ALL=(root) NOPASSWD: /opt/argon/bin/argon-deploy ""\n' > /etc/sudoers.d/argon-deploy.tmp
chmod 0440 /etc/sudoers.d/argon-deploy.tmp
visudo -cf /etc/sudoers.d/argon-deploy.tmp >/dev/null || { rm -f /etc/sudoers.d/argon-deploy.tmp; die "sudoers entry failed visudo"; }
mv /etc/sudoers.d/argon-deploy.tmp /etc/sudoers.d/argon-deploy

id -nG argon-deploy | grep -qw docker && die "argon-deploy is in the docker group: that is database superuser (deploy/README.md, Trust boundary)"
info "sudo for argon-deploy: $(sudo -l -U argon-deploy | sed -n '/may run/,$p' | tail -n +2 | tr -s ' ')"
ok "argon-deploy: one key, one command"
