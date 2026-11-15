#!/bin/sh
# Pinned upstream executables, verified before extraction. Update hashes with versions.
set -eu
if [ "$#" -ne 2 ]; then
    echo "usage: $0 amd64|arm64 OUTPUT_DIR" >&2
    exit 2
fi
arch=$1
output=$2
# shellcheck source=scripts/core-versions.sh
. "$(dirname -- "$0")/core-versions.sh"
case "$arch" in
    amd64)
        singbox_hash=699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968
        mihomo_arch=amd64-v1
        mihomo_hash=0c07340a01408d033dda4088ea2a4c1e4d82aeed7b2f84f7c046e93b6ff3290c
        ;;
    arm64)
        singbox_hash=436441f76ee2f7fe21b5b2d426d3cb261a480455a704865789ad533e4273ec26
        mihomo_arch=arm64
        mihomo_hash=daaf78a6240a282cdfd75bcbf98fe321f6bd52765cc29e79f344191198f0b50f
        ;;
    *)
        echo "unsupported architecture: $arch" >&2
        exit 2
        ;;
esac
mkdir -p "$output"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
curl -fL --retry 3 -o "$tmp/sing-box.tar.gz" "https://github.com/SagerNet/sing-box/releases/download/v$singbox_version/sing-box-$singbox_version-linux-$arch.tar.gz"
curl -fL --retry 3 -o "$tmp/mihomo.gz" "https://github.com/MetaCubeX/mihomo/releases/download/v$mihomo_version/mihomo-linux-$mihomo_arch-v$mihomo_version.gz"
(
    cd "$tmp"
    printf '%s  sing-box.tar.gz\n%s  mihomo.gz\n' "$singbox_hash" "$mihomo_hash" | sha256sum -c -
)
tar -xzf "$tmp/sing-box.tar.gz" -C "$tmp"
install -m 0755 "$tmp/sing-box-$singbox_version-linux-$arch/sing-box" "$output/sing-box"
gzip -dc "$tmp/mihomo.gz" > "$output/mihomo"
chmod 0755 "$output/mihomo"
curl -fL --retry 3 -o "$output/LICENSE-sing-box" "https://raw.githubusercontent.com/SagerNet/sing-box/v$singbox_version/LICENSE"
curl -fL --retry 3 -o "$output/LICENSE-mihomo" "https://raw.githubusercontent.com/MetaCubeX/mihomo/v$mihomo_version/LICENSE"
# sing-box's notice refers to the full GPL text; mihomo includes that text.
cp "$output/LICENSE-mihomo" "$output/COPYING"
