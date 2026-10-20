#!/usr/bin/env bash
set -euo pipefail
[[ $# == 5 ]] || {
    echo 'usage: build-packages.sh DISTRO VERSION ARCH INPUT_DIR OUTPUT_DIR' >&2
    exit 2
}
distro=$1 version=$2 arch=$3
case "$distro" in debian | arch | void) ;; *)
    echo 'unsupported distribution' >&2
    exit 2
    ;;
esac
[[ $version =~ ^[0-9]+([.][0-9]+)*([a-zA-Z0-9.+~]*)$ ]] || {
    echo 'invalid version' >&2
    exit 2
}
case "$arch" in
    amd64) native_arch=x86_64 ;;
    arm64) native_arch=aarch64 ;;
    *)
        echo 'unsupported architecture' >&2
        exit 2
        ;;
esac
[[ $distro != debian ]] || native_arch=$arch
input=$(cd "$4" && pwd)
mkdir -p "$5"
output=$(cd "$5" && pwd)
source_dir=$(cd "$(dirname "$0")/../packaging" && pwd)
for binary in ph sing-box mihomo; do
    [[ -f $input/$binary && -x $input/$binary ]] || {
        echo "missing executable: $binary" >&2
        exit 2
    }
done
for license in COPYING LICENSE-sing-box LICENSE-mihomo; do
    [[ -f $input/$license ]] || {
        echo "missing license: $license" >&2
        exit 2
    }
done
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
root=$work/root
mkdir -p "$root"/{usr/bin,usr/lib/ph,usr/share/doc/ph,etc/ph}
for binary in ph sing-box mihomo; do install -m 0755 "$input/$binary" "$root/usr/lib/ph/$binary"; done
install -m 0755 "$source_dir/common/ph" "$root/usr/bin/ph"
install -m 0755 "$source_dir/common/initialize" "$root/usr/lib/ph/initialize"
install -m 0644 "$source_dir/common/"*.json "$root/etc/ph/"
install -m 0644 "$source_dir/common/README" "$source_dir/common/copyright" "$input/COPYING" "$input/LICENSE-sing-box" "$input/LICENSE-mihomo" "$root/usr/share/doc/ph/"
conffiles='etc/ph/config.json etc/ph/sing-box.json etc/ph/mihomo.json'
if [[ $distro == void ]]; then
    sed 's/"systemctl", "restart", "ph-proxy.service"/"sv", "restart", "ph-proxy"/' "$source_dir/common/config.json" > "$root/etc/ph/config.json"
    mkdir -p "$root/etc/sv/"{ph-proxy,ph-proxy-update}
    install -m 0755 "$source_dir/void/proxy-run" "$root/etc/sv/ph-proxy/run"
    install -m 0755 "$source_dir/void/update-run" "$root/etc/sv/ph-proxy-update/run"
    install -m 0755 "$source_dir/void/log-initialize" "$root/usr/lib/ph/log-initialize"
    for service in ph-proxy ph-proxy-update; do
        mkdir -p "$root/etc/sv/$service/log"
        printf '#!/bin/sh\nexec svlogd -tt /var/log/%s\n' "$service" > "$root/etc/sv/$service/log/run"
        chmod 0755 "$root/etc/sv/$service/log/run"
    done
    install -m 0755 "$source_dir/void/"{INSTALL,REMOVE} "$root/"
else
    mkdir -p "$root/usr/lib/systemd/system"
    install -m 0644 "$source_dir/systemd/"ph-proxy* "$root/usr/lib/systemd/system/"
    install -m 0755 "$source_dir/systemd/install" "$root/usr/lib/ph/package-install"
    install -m 0755 "$source_dir/systemd/remove" "$root/usr/lib/ph/package-remove"
fi
case "$distro" in
    debian)
        mkdir "$root/DEBIAN"
        cat > "$root/DEBIAN/control" << META
Package: ph
Version: $version
Architecture: $native_arch
Maintainer: ph package maintainers
Depends: ca-certificates, systemd
Description: Subscription-managed proxy controller with bundled cores
META
        for file in $conffiles; do echo "/$file"; done > "$root/DEBIAN/conffiles"
        install -m 0755 "$source_dir/debian/"{postinst,prerm} "$root/DEBIAN/"
        dpkg-deb --root-owner-group --build "$root" "$output/ph_${version}_${native_arch}.deb"
        ;;
    arch)
        install -m 0644 "$source_dir/arch/install" "$root/.INSTALL"
        cat > "$root/.PKGINFO" << META
pkgname = ph
pkgbase = ph
pkgver = $version-1
pkgdesc = Subscription-managed proxy controller with bundled cores
builddate = $(date +%s)
packager = ph package maintainers
size = $(du -sk "$root" | awk '{print $1 * 1024}')
arch = $native_arch
depend = ca-certificates
depend = systemd
META
        for file in $conffiles; do echo "backup = $file"; done >> "$root/.PKGINFO"
        bsdtar --uid 0 --gid 0 --uname root --gname root -cf - -C "$root" .PKGINFO .INSTALL etc usr | zstd -T0 -o "$output/ph-${version}-1-${native_arch}.pkg.tar.zst"
        ;;
    void)
        (
            cd "$output"
            xbps-create -A "$native_arch" -n "ph-${version}_1" \
                -s 'Subscription-managed proxy controller with bundled cores' \
                -D 'ca-certificates>=0 runit>=0 procps-ng>=0' -F '/etc/ph/config.json /etc/ph/sing-box.json /etc/ph/mihomo.json' "$root"
        )
        ;;
esac
