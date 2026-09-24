# Client capability decision (sb-5iff)

**First additional backend: mihomo.** Its native selectable URL-test groups and
Clash REST endpoints match sb's selection model. Xray's JSON outbounds and gRPC
routing API require a separate controller; a Clash-shaped API alone never implies
config, DNS or route compatibility. sb remains a short-lived composer/controller,
not a proxy supervisor.

| Capability | mihomo | Xray |
| --- | --- | --- |
| Subscriptions | HTTP/file/inline native proxy providers consume compatible proxy definitions; sb instead retains its shared URI fetch/cache/policy and converts supported nodes. Arbitrary Clash YAML is **not** accepted by sb's URI parser. | JSON outbounds; no built-in generic HTTP subscription provider in the core configuration. Client-side conversion would be necessary. |
| Protocols | Native core supports SS, Trojan, VMess, VLESS and more; **sb currently imports only** basic URI-list SS, Trojan, VMess, VLESS with TCP/WS/gRPC, TLS/Reality where representable. Converter-specific options and other types fail closed. | Core supports its own outbound dialect; sb has no Xray renderer. |
| Live selection/latency | Select and URL-test groups; Clash REST `/proxies/proxy` and `/group/{name}/delay` used by sb. | gRPC routing/balancer and observatory, not drop-in Clash REST. |
| DNS/TUN | Native DNS and TUN; sb permits native DNS template settings but generates **no TUN**, bypass or tunnel controls. | Native DNS and TUN; OS routing and DNS hijack need separate integration. |
| Native validation | `mihomo -t -f CONFIG -d STATE_DIR`; installed candidate tested before commit. | `xray run -test -c CONFIG`. |
| Packaging | Official macOS/Linux releases, `pkgs.mihomo` in nixpkgs; sb offers a separate NixOS module. | Official macOS/Linux releases, `pkgs.xray` in nixpkgs; no sb module. |

Sources: [mihomo provider dialect](https://wiki.metacubex.one/en/config/proxy-providers/),
[API](https://wiki.metacubex.one/en/api/),
[proxy groups](https://wiki.metacubex.one/en/config/proxy-groups/url-test/),
[DNS](https://wiki.metacubex.one/en/config/dns/),
[TUN](https://wiki.metacubex.one/en/config/inbound/tun/),
[Xray configuration](https://xtls.github.io/en/config/),
[Xray routing](https://xtls.github.io/en/config/routing.html),
[Xray API](https://xtls.github.io/en/config/api.html),
[Xray TUN](https://xtls.github.io/en/config/inbounds/tun.html).

## Contract and limitations

`subscription` owns URL files, fetch, source policies and parsing into shared
`proxy.Node` values. `internal/app` owns the `Backend` port, private cache,
public manifest, modes, validation orchestration, install and restart argv.
`cmd/sb` selects a concrete `internal/backend` adapter at startup; adapters
own client-specific parsing, config composition, validation command and live
controller. `singbox` and `mihomo` own **different** config dialects; the optional sing-box converter, sing-box static/extra/legacy
outbounds and Linux `routing_mark` are unavailable on mihomo. A custom mihomo
template must be JSON (JSON is valid YAML) and cannot contain sing-box fields or
replace generated routing. Native mihomo providers/Clash YAML are not yet imported
by sb; hybrid sidecars and fail-closed firewall rules are out of scope.

Only fields explicitly converted from cached sing-box-shaped URI nodes are emitted:
name, type, server/port, credentials, VMess cipher/alterId, VLESS flow, TLS SNI,
ALPN, insecure flag, uTLS fingerprint, Reality public key/short ID, WS path/headers,
and gRPC service name. Unknown protocol/transport fields reject the candidate
rather than silently changing its security or routing semantics. Mihomo's `profile.store-selected` is forced off (an explicit `true` is
rejected): `use` remains live-only, while `pin` owns durable selection. A stale pin
remains saved but defaults to auto/manual when its leaf disappears. Use distinct
state dirs and listener ports for concurrent clients. Credentials and raw native
validation diagnostics are not printed by `sb update`; `sb check` forwards core
diagnostics explicitly.
