# NixOS and nix-darwin

Add this repository as a flake input named `ph`. Import
`ph.nixosModules.default` on NixOS or `ph.darwinModules.default` on macOS.
The flake also exports a default package and `overlays.default`.

## Managed profile

```nix
{ pkgs, ... }: {
  services.ph = {
    enable = true;
    backends = {
      "sing-box" = pkgs.sing-box;
      mihomo = pkgs.mihomo;
    };
    defaultBackend = "sing-box";
    converterPackage = null; # Native parser for shared caches.
    subscription = {
      enable = true;
      sources = [{
        name = "main";
        urlFile = "/run/agenix/provider-url";
        prefix = "main-";
      }];
    };
    refreshUser = "YOUR_USER"; # Optional fixed update request.
  };
}
```

Rebuild, then run `sudo ph update` to seed state and start the proxy.
The service cannot serve traffic before a validated config is installed.

- `ph-proxy` runs the selected core. Updates run daily by default.
- The wrapper uses `/etc/ph/config.json`. State defaults to `/var/lib/sing-box`.
- The mixed proxy listens at `127.0.0.1:1080`; the control API uses port 9090.
- Root owns credentials, registry, cache, preferences, and generated configs.
  Never use user-writable inputs for this root-run profile.
- Keep URLs in private secret files. Never put plaintext URLs in Nix expressions.
- The API trusts every local process. See [usage security](usage.md#security).

## Settings and authorization

- Without `backends`, the module provides sing-box only.
  `services.ph.package` overrides that core.
- `services.ph.settings` configures sing-box;
  `services.ph.mihomo.settings` configures mihomo.
- Switchable profiles require the shared loopback port 1080 and no sing-box TUN.
  For sing-box-only transparent routing, configure native DNS/TUN/routing explicitly.
- `refreshUser` may request `ph refresh` without sudo. It cannot pass arguments.
  NixOS authorizes only starting `ph-proxy-update.service`.
  Darwin watches a user-writable request file; the updater never parses it.
- Darwin refresh is asynchronous. Check `ph status` and `/var/log/ph-proxy.log`.
  NixOS logs are in `journalctl -u ph-proxy -u ph-proxy-update`.
- Persistent edits and client switches still require root.
  See [daily commands](usage.md#daily-commands).

`nixosModules.mihomo` also exports an independent `services.phMihomo` profile.
It uses `/var/lib/ph-mihomo` and a `ph-mihomo` wrapper. It does not support
client switching, TUN, or delegated refresh. Do not enable it alongside
`services.ph` on the same listener ports.
