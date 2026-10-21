#!/bin/sh
# Native sing-box TUN/DNS traffic, isolated from the container's control network.
set -eu
distro=$1
fail() { echo "FAIL: routing: $*" >&2; exit 1; }
service_control() {
    if [ "$distro" = void ]; then
        case "$1" in stop) sv -w 10 stop /var/service/ph-proxy ;; start) sv up /var/service/ph-proxy ;; esac
    else
        systemctl "$1" ph-proxy.service
    fi
}
http() {
    [ "$(curl --fail --silent --show-error --max-time 3 --noproxy '*' "$1")" = fixture-ok ]
}
dns() {
    answer=$(dig +time=2 +tries=1 +short "$@" @203.0.113.53 "$name") || return
    [ "$answer" = 198.18.0.1 ]
}
wait_http() {
    for _ in $(seq 1 30); do
        if http http://routed.test:18080/health; then return; fi
        sleep 1
    done
    fail 'routed HTTP did not recover'
}
start_server() {
    ip netns exec ph-server /usr/lib/ph/sing-box run -c /tmp/routing-server.json > /tmp/routing-server.log 2>&1 &
    server_pid=$!
}
# Preserve the managed configuration, subscription registry and durable selection.
template=$(python3 -c 'import json; print(json.load(open("/etc/ph/config.json"))["backends"]["sing-box"]["template_file"])')
backup=$(mktemp -d)
cp "$template" "$backup/template"
cp /var/lib/ph/subscriptions.json "$backup/subscriptions.json"
cp /var/lib/ph/pin.json "$backup/pin.json"
cp /etc/resolv.conf "$backup/resolv.conf"
cleanup() {
    result=$?
    trap - EXIT
    cat "$backup/resolv.conf" > /etc/resolv.conf
    service_control stop || :
    if ip netns list | grep -q '^ph-server '; then
        ip netns pids ph-server | xargs -r kill
        ip netns del ph-server
    fi
    ip link del ph-client 2>/dev/null || :
    cp "$backup/template" "$template"
    cp "$backup/subscriptions.json" /var/lib/ph/subscriptions.json
    cp "$backup/pin.json" /var/lib/ph/pin.json
    rm -f /etc/ph/routed-url
    ph update || result=1
    service_control start || result=1
    rm -rf "$backup"
    exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
ip netns add ph-server
ip link add ph-client type veth peer name ph-peer
ip link set ph-peer netns ph-server
ip addr add 192.0.2.1/30 dev ph-client
ip link set ph-client up
ip -n ph-server link set lo up
ip -n ph-server addr add 192.0.2.2/30 dev ph-peer
ip -n ph-server link set ph-peer up
ip -n ph-server addr add 198.18.0.1/32 dev lo
cat > /tmp/routing-server.json << 'JSON'
{"inbounds":[{"type":"shadowsocks","listen":"0.0.0.0","listen_port":18388,"method":"aes-128-gcm","password":"fixture-secret"}],"outbounds":[{"type":"direct"}]}
JSON
start_server
ip netns exec ph-server python3 /project/tests/distros/fixture.py --listen 0.0.0.0 --server 192.0.2.2 > /tmp/routing-fixture.log 2>&1 &
ip netns exec ph-server dnsmasq --no-daemon --no-resolv --no-hosts --address=/routed.test/198.18.0.1 --listen-address=198.18.0.1 --bind-interfaces > /tmp/routing-dns.log 2>&1 &
for _ in $(seq 1 30); do
    if http http://192.0.2.2:18080/health; then break; fi
    sleep 1
done
http http://192.0.2.2:18080/health || fail 'namespace fixture unavailable'
if http http://198.18.0.1:18080/health; then fail 'direct HTTP reachable before TUN'; fi
name=before.routed.test
if dns; then fail 'direct DNS reachable before TUN'; fi
umask 077
printf '%s\n' http://192.0.2.2:18080/subscription > /etc/ph/routed-url
ph subscription delete fixture
ph subscription add --id routed --url-file /etc/ph/routed-url
ph update
tag=$(python3 -c 'import json; print(json.load(open("/var/lib/ph/subscription.json"))["nodes"][0]["tag"])')
ph pin "$tag"
cp /project/tests/routing/sing-box.json "$template"
ph apply
printf 'nameserver 203.0.113.53\n' > /etc/resolv.conf
wait_http
ip route get 198.18.0.1 | grep -E 'dev ph-tun( |$)'
name=routed.test
dns || fail 'UDP DNS did not resolve through TUN'
dns +tcp || fail 'TCP DNS did not resolve through TUN'
kill "$server_pid"
wait "$server_pid" || :
# Use the literal target so a DNS failure cannot hide an HTTP bypass.
if http http://198.18.0.1:18080/health; then fail 'HTTP bypassed stopped server'; fi
name=offline.routed.test
if dns; then fail 'DNS bypassed stopped server'; fi
if dns +tcp; then fail 'TCP DNS bypassed stopped server'; fi
# The local managed core must remain alive despite the unavailable remote server.
ph status
if [ "$distro" = void ]; then sv status /var/service/ph-proxy; else systemctl is-active --quiet ph-proxy.service; fi
start_server
wait_http
name=recovered.routed.test
dns || fail 'DNS did not recover'
dns +tcp || fail 'TCP DNS did not recover'
service_control stop
if ip link show ph-tun; then fail 'TUN survived client stop'; fi
if http http://198.18.0.1:18080/health; then fail 'direct HTTP reachable after client stop'; fi
name=stopped.routed.test
if dns; then fail 'DNS reachable after client stop'; fi
echo 'PASS: sing-box native TUN and UDP/TCP DNS routing'
