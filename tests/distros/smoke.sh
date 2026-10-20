#!/bin/sh
set -eu
distro=$1
fail() {
    echo "FAIL: $*" >&2
    exit 1
}
expect_failure() { if "$@"; then fail "unexpected success: $*"; fi; }
install_package() {
    case "$distro" in
        debian) dpkg -i /artifacts/"$1"/*.deb ;;
        arch) pacman --disable-sandbox -U --noconfirm /artifacts/"$1"/*.pkg.tar.zst ;;
        void)
            if xbps-query ph > /dev/null 2>&1; then
                xbps-install -yu --repository /artifacts/"$1" ph
            else
                xbps-install -y --repository /artifacts/"$1" ph
            fi
            ;;
    esac
}
proxy() {
    response=$(curl --fail --silent --show-error --max-time 3 --noproxy '' --proxy http://127.0.0.1:1080 http://127.0.0.1:18080/health) || return
    [ "$response" = fixture-ok ]
}
wait_proxy() {
    attempt=0
    while [ "$attempt" -lt 30 ]; do
        attempt=$((attempt + 1))
        if proxy; then return; fi
        sleep 1
    done
    fail 'proxy traffic timed out'
}
core_pid() {
    python3 - << 'PY'
from pathlib import Path
for p in Path('/proc').glob('[0-9]*/cmdline'):
    args = p.read_bytes().split(b'\0')
    if b'/var/lib/ph/config.json' in args and any(binary in args for binary in (b'/usr/lib/ph/sing-box', b'/usr/lib/ph/mihomo')):
        print(p.parent.name)
        break
else:
    raise SystemExit('managed core not running')
PY
}
update_service() {
    previous=$(core_pid)
    ph refresh
    attempt=0
    while [ "$attempt" -lt 30 ]; do
        attempt=$((attempt + 1))
        current=$(core_pid 2> /dev/null) || current=
        if [ -n "$current" ] && [ "$current" != "$previous" ]; then
            wait_proxy
            return
        fi
        sleep 1
    done
    fail 'scheduled update did not restart the managed core'
}
private_file() {
    python3 - "$1" << 'PY'
import os, stat, sys
s = os.stat(sys.argv[1])
assert s.st_uid == 0 and stat.S_IMODE(s.st_mode) == 0o600, (sys.argv[1], s)
PY
}
install_package 0.0.1
[ "$(ph version)" = 0.0.1 ]
expect_failure ph refresh fixture
[ "$(readlink -f "$(command -v ph)")" = /usr/bin/ph ]
test ! -e /var/lib/ph/config.json
case "$distro" in
    void)
        test -L /var/service/ph-proxy
        test -L /var/service/ph-proxy-update
        attempt=0
        while [ "$attempt" -lt 30 ]; do
            attempt=$((attempt + 1))
            if sv status /var/service/ph-proxy /var/service/ph-proxy-update; then break; fi
            sleep 1
        done
        sv status /var/service/ph-proxy /var/service/ph-proxy-update
        ;;
    *)
        systemctl is-enabled ph-proxy.service
        systemctl is-enabled ph-proxy-update.timer
        expect_failure systemctl is-active --quiet ph-proxy.service
        ;;
esac
python3 /project/tests/distros/fixture.py > /tmp/fixture.log 2>&1 &
cat > /tmp/server.json << 'JSON'
{"inbounds":[{"type":"shadowsocks","listen":"127.0.0.1","listen_port":18388,"method":"aes-128-gcm","password":"fixture-secret"}],"outbounds":[{"type":"direct"}]}
JSON
/usr/lib/ph/sing-box run -c /tmp/server.json > /tmp/server.log 2>&1 &
# Edit managed configuration/templates so upgrades must preserve real local changes.
python3 - << 'PY'
import json
from pathlib import Path
p = Path('/etc/ph/config.json')
s = json.loads(p.read_text())
s['test_url'] = 'http://127.0.0.1:18080/health'
p.write_text(json.dumps(s, indent=2) + '\n')
for backend in s['backends'].values():
    p = Path(backend['template_file'])
    p.write_text(p.read_text() + '\n')
PY
umask 077
printf '%s\n' http://127.0.0.1:18080/subscription > /etc/ph/fixture-url
ph subscription add --id fixture --url-file /etc/ph/fixture-url
private_file /etc/ph/fixture-url
attempt=0
while [ "$attempt" -lt 30 ]; do
    attempt=$((attempt + 1))
    if curl -fsS --max-time 1 http://127.0.0.1:18080/health > /dev/null; then break; fi
    sleep 1
done
ph update
wait_proxy
ph status
ph list
# The manifest gives the actual imported tag, not an assumed converter naming rule.
tag=$(python3 -c 'import json; print(json.load(open("/var/lib/ph/subscription.json"))["nodes"][0]["tag"])')
ph use "$tag"
ph pin "$tag"
ph apply
wait_proxy
chmod 0644 /var/lib/ph/subscriptions.json
/usr/lib/ph/initialize
private_file /var/lib/ph/subscriptions.json
private_file /var/lib/ph/cache.json
private_file /var/lib/ph/config.json
private_file /var/lib/ph/pin.json
useradd -m tester
for backend in mihomo sing-box; do
    ph backend select "$backend"
    wait_proxy
    ph test
    su -s /bin/sh tester -c 'ph list && ph status'
    su -s /bin/sh tester -c "ph use '$tag'"
    expect_failure su -s /bin/sh tester -c 'cat /var/lib/ph/cache.json'
    expect_failure su -s /bin/sh tester -c 'cat /var/lib/ph/config.json'
    expect_failure su -s /bin/sh tester -c "ph pin '$tag'"
    expect_failure su -s /bin/sh tester -c 'ph subscription delete fixture'
    python3 - "$tag" << 'PY'
import json, sys
m = json.load(open('/var/lib/ph/subscription.json'))
assert m['pinnedTag'] == sys.argv[1] and m['pinActive']
PY
    update_service
    wait_proxy
done
# Failed-all-source updates cannot replace the installed configuration or stop traffic.
sha256sum /var/lib/ph/config.json > /tmp/config.sha
touch /tmp/fail-source
expect_failure ph update
sha256sum -c /tmp/config.sha
wait_proxy
rm /tmp/fail-source
# Snapshot all persistent user state except derived health and manifest timestamps.
python3 - << 'PY'
import hashlib, json
from pathlib import Path
paths = list(Path('/etc/ph').rglob('*'))
paths += [Path('/var/lib/ph') / name for name in ('cache.json', 'pin.json', 'active-backend.json', 'subscriptions.json', 'mode.json', 'overrides.json')]
json.dump({str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths if p.is_file()}, open('/tmp/preserved.json', 'w'))
PY
previous=$(core_pid)
install_package 0.0.2
[ "$(ph version)" = 0.0.2 ]
wait_proxy
[ "$(core_pid)" != "$previous" ] || fail 'upgrade did not restart the active core'
case "$distro" in
    debian) test "$(dpkg-query -W -f='${Version}' ph)" = 0.0.2 ;;
    arch) pacman -Q ph | grep -E '^ph 0\.0\.2(-[0-9]+)?$' ;;
    void) xbps-query -p pkgver ph | grep -E '^ph-0\.0\.2_[0-9]+$' ;;
esac
python3 - << 'PY'
import hashlib, json
from pathlib import Path
for name, digest in json.load(open('/tmp/preserved.json')).items():
    assert hashlib.sha256(Path(name).read_bytes()).hexdigest() == digest, name
PY
wait_proxy
ph status
ph list
case "$distro" in
    debian) dpkg -r ph ;;
    arch) pacman --disable-sandbox -R --noconfirm ph ;;
    void) xbps-remove -y ph ;;
esac
attempt=0
while [ "$attempt" -lt 30 ]; do
    attempt=$((attempt + 1))
    if ! proxy; then break; fi
    sleep 1
done
expect_failure proxy
test -s /var/lib/ph/cache.json
test -s /var/lib/ph/pin.json
case "$distro" in
    arch) test -s /etc/ph/config.json.pacsave ;;
    *) test -s /etc/ph/config.json ;;
esac
case "$distro" in
    void)
        test ! -L /var/service/ph-proxy
        test ! -L /var/service/ph-proxy-update
        ;;
    *)
        expect_failure systemctl is-active --quiet ph-proxy.service
        expect_failure systemctl is-active --quiet ph-proxy-update.timer
        ;;
esac
echo "PASS: $distro managed lifecycle (both real cores)"
