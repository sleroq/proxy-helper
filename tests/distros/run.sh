#!/bin/sh
set -eu
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || { echo 'usage: tests/distros/run.sh debian|arch|void INPUT_DIR [ARCH]' >&2; exit 2; }
distro=$1
input=$(cd "$2" && pwd)
arch=${3:-$(uname -m)}
case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
case "$distro" in
 debian|arch) init=/sbin/init ;;
 void) init=/usr/bin/runsvdir ;;
 *) echo "unsupported distro: $distro" >&2; exit 2 ;;
esac
root=$(CDPATH='' cd -- "$(dirname "$0")/../.." && pwd)
[ -f "$root/scripts/build-packages.sh" ] || { echo 'missing scripts/build-packages.sh' >&2; exit 1; }
for file in ph sing-box mihomo LICENSE-sing-box LICENSE-mihomo COPYING; do test -s "$input/$file"; done
output=$(mktemp -d)
name=ph-distro-$distro-$$
image=ph-distro-builder:$distro-$arch
cleanup() {
 result=$?
 if [ "$result" -ne 0 ]; then
  docker logs "$name" >&2 || :
  docker exec "$name" timeout -k 1 10 sh -c 'cat /tmp/*.log; if command -v journalctl >/dev/null; then journalctl --no-pager -n 150; else sv status /var/service/*; ls -l /var/service /etc/sv/*/supervise; cat /var/log/ph-proxy*/current; fi' >&2 || :
 fi
 docker rm -f "$name" >/dev/null 2>&1 || :
 rm -rf "$output"
 exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
for version in 0.0.1 0.0.2; do
 version_input=$output/input-$version
 mkdir "$version_input"
 for file in sing-box mihomo LICENSE-sing-box LICENSE-mihomo COPYING; do cp "$input/$file" "$version_input/$file"; done
 (cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$version_input/ph" ./cmd/ph)
 "$root/tests/distros/build.sh" "$distro" "$version" "$arch" "$version_input" "$output/$version"
done
if [ "$distro" = void ]; then
 docker run -dt --platform "linux/$arch" --name "$name" --privileged -v "$root:/project:ro" -v "$output:/artifacts:ro" "$image" "$init" /var/service >/dev/null
else
 docker run -dt --platform "linux/$arch" --name "$name" --privileged -e container=docker --cgroupns private --tmpfs /run --tmpfs /run/lock -v "$root:/project:ro" -v "$output:/artifacts:ro" "$image" "$init" --unit=basic.target >/dev/null
fi
ready=false
attempt=0
while [ "$attempt" -lt 10 ]; do
 attempt=$((attempt + 1))
 if [ "$(docker inspect -f '{{.State.Running}}' "$name")" != true ]; then fail_message="supervisor exited before readiness"; break; fi
 if docker exec "$name" sh -c 'if command -v systemctl >/dev/null; then timeout -k 1 5 systemctl show-environment >/dev/null; else test "$(cat /proc/1/comm)" = runsvdir; fi'; then ready=true; break; fi
 sleep 1
done
[ "$ready" = true ] || { echo "${fail_message:-supervisor readiness timed out}" >&2; exit 1; }
docker exec "$name" timeout -k 5 300 sh /project/tests/distros/smoke.sh "$distro"
