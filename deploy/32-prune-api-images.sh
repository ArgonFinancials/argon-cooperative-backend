#!/usr/bin/env bash
# THE SERVER. Removes old API images (2026-11-21: 24 of them, seven weeks, on
# a 3.7 GB disk). Keeps the newest three (the current release and the two
# before it, the rollback depth), and ALWAYS keeps the image a running API or
# wallet container uses and the releases /opt/argon/src and /opt/argon/src.old
# name. Touches nothing outside the argon-api repository. A removal that
# Docker refuses (a stopped container still uses it) is reported, not forced.
#
#   sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/32-prune-api-images.sh            # shows what it would remove
#   sudo ARGON_ENV=/etc/argon/deploy.env bash deploy/32-prune-api-images.sh --confirm  # removes it
#
# argon-deploy runs it with --confirm after a deploy succeeds; its failure
# never fails a deploy. PRUNE_REPO and PRUNE_RELEASES are the tests' (another
# repository, other release files).
source "$(dirname "$0")/lib.sh"
set +E
confirm=0; [[ ${1:-} == --confirm ]] && confirm=1
repo=${PRUNE_REPO:-argon-api}
keep=3

# Newest first, by when each image was made.
mapfile -t refs < <(docker images "$repo" --format '{{.CreatedAt}}|{{.Repository}}:{{.Tag}}' | grep -v ':<none>$' | sort -r | cut -d'|' -f2-) ||
  die "cannot list the $repo images"
declare -A protect=()
for i in "${!refs[@]}"; do ((i < keep)) && protect[${refs[$i]}]="one of the newest $keep"; done
while read -r img; do protect[$img]="a running container uses it"; done < <(docker ps --format '{{.Image}}')
for f in ${PRUNE_RELEASES:-/opt/argon/src/RELEASE /opt/argon/src.old/RELEASE}; do
  [[ -f $f ]] && protect[$repo:$(tr -d '[:space:]' <"$f")]="named by $f"
done

gone=() kept=0
for r in "${refs[@]}"; do
  if [[ -n ${protect[$r]:-} ]]; then kept=$((kept + 1)); continue; fi
  gone+=("$r")
done
info "$repo: ${#refs[@]} images; keeping $kept; $( ((${#gone[@]})) && echo "removing ${#gone[@]}: ${gone[*]#"$repo:"}" || echo "nothing to remove")"
((confirm)) || { ((${#gone[@]})) && info "nothing removed: run again with --confirm"; exit 0; }
failed=0
for r in "${gone[@]}"; do
  docker rmi "$r" >/dev/null 2>&1 || { info "not removed: $r (Docker refused; a stopped container may use it)"; failed=1; }
done
ok "$repo: removed $(( ${#gone[@]} )) old image(s)$( ((failed)) && echo ", some refused (above)")"
((failed == 0))
