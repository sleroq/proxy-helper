ARG BASE=debian:trixie
FROM ${BASE}
ARG DISTRO
RUN case "$DISTRO" in \
 debian) apt-get update && apt-get install -y systemd systemd-sysv python3 curl procps ;; \
 arch) pacman --disable-sandbox -Syu --noconfirm && pacman --disable-sandbox -S --noconfirm libarchive zstd python curl systemd procps-ng ;; \
 void) xbps-install -Sy && xbps-install -y bash python3 curl runit shadow procps-ng && mkdir -p /run/runit/runsvdir/current /var/service ;; \
 *) exit 1 ;; esac
# Test init must not prompt for setup or reset the host's shared binfmt registrations.
RUN if [ "$DISTRO" != void ]; then systemctl mask systemd-firstboot.service systemd-binfmt.service; fi
STOPSIGNAL SIGRTMIN+3
