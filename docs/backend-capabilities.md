# Supported clients

ph supports sing-box and mihomo. Both use shared subscriptions, caches,
filters, automatic groups, live selection, and persistent pins.

| Feature | sing-box | mihomo |
| --- | --- | --- |
| Native URI subscriptions | Yes | Yes |
| Optional `sing-box-sub` converter | Yes | No |
| Native JSON template | Yes | Yes |
| Static/extra outbounds | Yes | No |
| Bypass and TUN controls | Yes, with a suitable template | No |
| Linux routing mark | Yes | No |

## Subscription formats

- Plain or base64 URI lists: Shadowsocks, Trojan, VLESS, and base64-JSON VMess.
- Basic TCP, WebSocket, gRPC, TLS, and Reality parameters.
- No Clash YAML, native mihomo providers, SSR, Hysteria, or TUIC imports.
- Malformed or unsupported entries are skipped. An unusable source keeps its cache.
- Mihomo rejects node options it cannot translate. Core support alone does not
  imply ph support.

For sing-box-only providers, set `converter` to an installed `sing-box-sub`
executable. Use the native parser for shared, switchable caches.

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
