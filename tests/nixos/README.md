# NixOS subscription and routing tests

Run on a Linux builder with KVM and Nix flakes enabled:

```sh
nix build -L .#checks.x86_64-linux.nixos-proxy
# On a native aarch64 Linux builder:
nix build -L .#checks.aarch64-linux.nixos-proxy
```

The VM test imports the shipped NixOS modules and uses real cores from the
locked nixpkgs input. It covers:

- `services.ph`: HTTP subscription import, traffic through both sing-box and
  mihomo, backend switching, persistent pins, and the update service/timer.
- `services.phMihomo`: subscription import, proxy traffic, and service updates.
- Sing-box-only `services.ph`: native TUN auto-routing, transparent IPv4 TCP,
  UDP/TCP DNS interception, and HTTP using the system resolver.

A separate server VM hosts the subscription, Shadowsocks, HTTP, and DNS fixtures.
The routed target is a server-local address with no direct path from the client.
HTTP and DNS must fail before TUN starts. Stopping only Shadowsocks must break
traffic and fresh DNS queries while the managed core stays running; restarting
it must restore both. Stopping the client must remove its TUN and routes.

The TUN template is shared with the [native distro tests](../distros/README.md).
The NixOS TUN node trusts `ph-tun` in its firewall so the native TCP stack can
accept redirected connections. The firewall and strict reverse-path filtering
remain enabled. It captures only test IPv4 destinations, not the
subscription/control network.
These tests do not claim IPv6, every application protocol, Internet provider
compatibility, or a firewall kill switch. Managed mihomo rejects TUN; its tests
use the mixed proxy. No Darwin service or routing test is included.

CI runs this VM test on x86_64 Linux with KVM, in addition to the Debian, Arch,
and Void lifecycle/routing matrix. The aarch64 NixOS check is available for
native builders but is not run by CI.
