# Supported clients

ph supports sing-box and mihomo. Both use shared subscriptions, caches,
filters, automatic groups, live selection, and persistent pins.

| Feature | sing-box | mihomo |
| --- | --- | --- |
| Native URI subscriptions | Yes | Yes |
| Native JSON template | Yes | Yes |
| Static/extra outbounds | Yes | No |
| Bypass and TUN controls | Yes, with a suitable template | No |
| Linux routing mark | Yes | No |

## Subscription formats

- Plain or base64 URI lists: Shadowsocks, Trojan, VLESS, base64-JSON VMess,
  and Hysteria 2 (`hysteria2://` or `hy2://`).
- Basic TCP, WebSocket, gRPC, TLS, and Reality parameters.
- Hysteria 2 supports password and `username:password` authentication, a single
  server port (default 443), TLS SNI, ALPN, `insecure=0/1`, and Salamander
  obfuscation (`obfs=salamander` and `obfs-password`). Both protocol names work
  in protocol exclusions. Other Hysteria 2 query options, port hopping, Gecko
  obfuscation, certificate pins, ECH, and realm links are not imported.
- No Clash YAML, native mihomo providers, SSR, Hysteria 1, or TUIC imports.
- Malformed or unsupported entries are skipped. An unusable source keeps its cache.
- Mihomo rejects node options it cannot translate. Core support alone does not
  imply ph support.

Subscriptions use the native URI parser for both clients and share the same cache.

## Templates and switching

- Client templates use different schemas. Do not reuse one across clients.
- ph owns mihomo proxies, groups, rules, mode, and TUN settings.
  Native DNS settings are allowed.
- Mihomo's `profile.store-selected` must stay off. Use `ph pin` for saved selection.
- Switching runs one core at a time. It validates, restarts, and checks readiness.
  On activation failure, ph attempts to restore the previous core and config.
- Incompatible modes, TUN settings, or outbounds can block a switch.
  Nothing translates host routing or firewall policy.

See [usage](usage.md#switch-clients) for commands and standalone configuration.
