#!/bin/sh
# Pinned upstream executables, verified before extraction. Update hashes with versions.
set -eu
if [ "$#" -ne 2 ]; then
    echo "usage: $0 amd64|arm64 OUTPUT_DIR" >&2
    exit 2
fi
arch=$1
output=$2
singbox_version=1.14.3
mihomo_version=1.19.32
case "$arch" in
    amd64)
        singbox_hash=e2bdf179c15a3652955dc44867e33ca0965c9c9b91b34267dcc5a5d639a5feee
        mihomo_arch=amd64-v1
        mihomo_hash=306f81e723e60ce6b828899a6fe83e1d00e9ecefb2dc8d4d849312a5bc00efdc
        ;;
    arm64)
        singbox_hash=29dae24d76bea3bc62d07dd3e60cfc12003de7debbd6b768df9be9c68360174e
        mihomo_arch=arm64
        mihomo_hash=9dd862e28b46ff7d775f169cceebc28deccaa0a9e804237d421cd2571e0caba0
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
