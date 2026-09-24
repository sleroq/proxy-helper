{
  config,
  lib,
  pkgs,
  sbFlake,
  ...
}:
let
  cfg = config.services.sb;
  sub = cfg.subscription;
  json = pkgs.formats.json { };
  state = cfg.stateDir;
  singBoxInbounds =
    cfg.settings.inbounds or [
      {
        type = "mixed";
        tag = "mixed-in";
        listen = "127.0.0.1";
        listen_port = 1080;
      }
    ];
  namedSources = lib.imap0 (
    i: source: source // { name = if source.name == "" then "src${toString i}" else source.name; }
  ) sub.sources;
  template = json.generate "sing-box-template.json" (
    removeAttrs (lib.recursiveUpdate {
      inbounds = singBoxInbounds;
      dns = {
        servers = [
          {
            type = "local";
            tag = "local-dns";
          }
        ];
        final = "local-dns";
      };
      route.final = "proxy";
      experimental = lib.optionalAttrs (
        sub.enable || builtins.length (builtins.attrNames cfg.backends) > 1
      ) { clash_api.external_controller = sub.apiAddress; };
    } cfg.settings) [ "outbounds" ]
  );
  mihomoTemplate = json.generate "mihomo-template.json" (
    {
      mixed-port = 1080;
      bind-address = "127.0.0.1";
      allow-lan = false;
    }
    // lib.optionalAttrs (sub.enable || builtins.length (builtins.attrNames cfg.backends) > 1) {
      external-controller = sub.apiAddress;
    }
    // cfg.mihomo.settings
  );
  static = json.generate "sing-box-static-outbounds.json" cfg.staticOutbounds;
  declared = json.generate "sing-box-subscriptions.json" {
    subscriptions = map (source: {
      id = source.name;
      url_file = source.urlFile;
      inherit (source) prefix;
      disabled = !source.enable;
      auto = source.autoSelect;
      include_tags = source.includeTags;
      exclude_tags = source.excludeTags;
      include_servers = source.includeServers;
      exclude_servers = source.excludeServers;
    }) namedSources;
  };
  settings = json.generate "sb-config.json" (
    {
      template_file = toString template;
      backend = cfg.defaultBackend;
      backends = lib.mapAttrs (name: package: {
        template_file = toString (if name == "sing-box" then template else mihomoTemplate);
        binary = "${package}/bin/${if name == "sing-box" then "sing-box" else "mihomo"}";
      }) cfg.backends;
      state_dir = state;
      api_url = "http://${sub.apiAddress}";
      test_url = sub.testURL;
      test_interval = sub.testInterval;
      tolerance = sub.tolerance;
      sing_box =
        if builtins.hasAttr "sing-box" cfg.backends then "${cfg.backends."sing-box"}/bin/sing-box" else "";
      converter =
        if builtins.hasAttr "sing-box" cfg.backends && cfg.converterPackage != null then
          "${cfg.converterPackage}/bin/sing-box-sub"
        else
          "";
      exclude_protocols = sub.excludeProtocols;
      exclude_node_names = sub.excludeNodeNames;
      overrides_file = "${state}/overrides.json";
      stores =
        lib.optional (namedSources != [ ]) {
          name = "nix";
          path = toString declared;
          writable = false;
        }
        ++ sub.stores;
      restart_command =
        if pkgs.stdenv.hostPlatform.isLinux then
          [
            "${pkgs.systemd}/bin/systemctl"
            "restart"
            "sb-proxy.service"
          ]
        else
          [
            "/bin/launchctl"
            "kickstart"
            "-k"
            "system/org.nixos.sb-proxy"
          ];
    }
    // lib.optionalAttrs (cfg.staticOutbounds != [ ]) { static_outbounds_file = toString static; }
    // lib.optionalAttrs (cfg.extraOutboundsFile != null) {
      extra_outbounds_file = cfg.extraOutboundsFile;
    }
    // lib.optionalAttrs (cfg.legacyOutboundsFile != null) {
      legacy_outbounds_file = cfg.legacyOutboundsFile;
    }
    // lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux { routing_mark = cfg.routingMark; }
  );
  sb = pkgs.writeShellScriptBin "sb" ''
    if [ "''${1-}" = refresh ]; then
      if [ "$#" -ne 1 ]; then echo "sb refresh takes no arguments" >&2; exit 2; fi
      ${
        if pkgs.stdenv.hostPlatform.isLinux then
          "exec ${pkgs.systemd}/bin/systemctl start sb-proxy-update.service"
        else
          "exec ${pkgs.coreutils}/bin/touch ${lib.escapeShellArg "${state}/refresh-request"}"
      }
    fi
    exec ${cfg.cliPackage}/bin/sb --config ${settings} "$@"
  '';
  update = pkgs.writeShellScript "sb-proxy-update" ''
    exec ${sb}/bin/sb update
  '';
  runner = pkgs.writeShellScript "sb-proxy-run" ''
    exec ${sb}/bin/sb run
  '';
  sourceType = lib.types.submodule {
    options = {
      name = lib.mkOption {
        type = lib.types.str;
        default = "";
      };
      urlFile = lib.mkOption { type = lib.types.str; };
      prefix = lib.mkOption {
        type = lib.types.str;
        default = "";
      };
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
      };
      autoSelect = lib.mkOption {
        type = lib.types.bool;
        default = true;
      };
      includeTags = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
      };
      excludeTags = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
      };
      includeServers = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
      };
      excludeServers = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
      };
    };
  };
in
{
  options.services.sb = {
    enable = lib.mkEnableOption "proxy client managed by sb";
    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.sing-box;
    };
    backends = lib.mkOption {
      type = lib.types.attrsOf lib.types.package;
      default = {
        "sing-box" = cfg.package;
      };
      description = "Installed cores available for managed backend selection.";
    };
    defaultBackend = lib.mkOption {
      type = lib.types.enum [
        "sing-box"
        "mihomo"
      ];
      default = "sing-box";
    };
    mihomo.settings = lib.mkOption {
      type = lib.types.submodule { freeformType = json.type; };
      default = { };
      description = "Native mihomo template settings (not sing-box settings).";
    };
    cliPackage = lib.mkOption {
      type = lib.types.package;
      default = pkgs.sb or sbFlake.packages.${pkgs.stdenv.hostPlatform.system}.sb;
    };
    converterPackage = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = pkgs.sing-box-subscribe-cli or null;
    };
    stateDir = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/sing-box";
    };
    staticOutbounds = lib.mkOption {
      type = lib.types.listOf json.type;
      default = [ ];
    };
    extraOutboundsFile = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
    };
    legacyOutboundsFile = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
    };
    refreshUser = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
    };
    routingMark = lib.mkOption {
      type = lib.types.ints.unsigned;
      default = 0;
    };
    extraCapabilities = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
    };
    settings = lib.mkOption {
      type = lib.types.submodule { freeformType = json.type; };
      default = { };
    };
    subscription = lib.mkOption {
      default = { };
      type = lib.types.submodule {
        options = {
          enable = lib.mkEnableOption "managed subscriptions";
          sources = lib.mkOption {
            type = lib.types.listOf sourceType;
            default = [ ];
          };
          stores = lib.mkOption {
            type = lib.types.listOf (
              lib.types.submodule {
                options = {
                  name = lib.mkOption { type = lib.types.str; };
                  path = lib.mkOption { type = lib.types.str; };
                  writable = lib.mkOption {
                    type = lib.types.bool;
                    default = false;
                  };
                };
              }
            );
            default = [
              {
                name = "local";
                path = "${state}/subscriptions.json";
                writable = true;
              }
            ];
          };
          updateInterval = lib.mkOption {
            type = lib.types.ints.positive;
            default = 86400;
          };
          apiAddress = lib.mkOption {
            type = lib.types.str;
            default = "127.0.0.1:9090";
          };
          testURL = lib.mkOption {
            type = lib.types.str;
            default = "https://www.gstatic.com/generate_204";
          };
          testInterval = lib.mkOption {
            type = lib.types.str;
            default = "5m";
          };
          tolerance = lib.mkOption {
            type = lib.types.ints.unsigned;
            default = 50;
          };
          excludeProtocols = lib.mkOption {
            type = lib.types.str;
            default = "ssr";
          };
          excludeNodeNames = lib.mkOption {
            type = lib.types.str;
            default = "";
          };
        };
      };
    };
  };
  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion =
          cfg.backends != { }
          && lib.all (
            name:
            builtins.elem name [
              "sing-box"
              "mihomo"
            ]
          ) (builtins.attrNames cfg.backends);
        message = "services.sb.backends must contain only sing-box and/or mihomo and cannot be empty.";
      }
      {
        assertion = builtins.hasAttr cfg.defaultBackend cfg.backends;
        message = "services.sb.defaultBackend must be included in services.sb.backends.";
      }
      {
        assertion =
          !(
            cfg.mihomo.settings ? proxies
            || cfg.mihomo.settings ? proxy-groups
            || cfg.mihomo.settings ? proxy-providers
            || cfg.mihomo.settings ? rules
            || cfg.mihomo.settings ? mode
            || cfg.mihomo.settings ? tun
          );
        message = "sb manages mihomo proxies, groups, rules and mode; native providers and TUN are unsupported.";
      }
      {
        assertion =
          lib.attrByPath [ "allow-lan" ] false cfg.mihomo.settings == false
          && lib.attrByPath [ "bind-address" ] "127.0.0.1" cfg.mihomo.settings == "127.0.0.1"
          && (
            !(sub.enable || builtins.length (builtins.attrNames cfg.backends) > 1)
            || lib.attrByPath [ "external-controller" ] sub.apiAddress cfg.mihomo.settings == sub.apiAddress
          )
          && lib.hasPrefix "127.0.0.1:" (
            lib.attrByPath [ "external-controller" ] "127.0.0.1:9090" cfg.mihomo.settings
          )
          && lib.attrByPath [ "mixed-port" ] 1080 cfg.mihomo.settings == 1080;
        message = "The managed mihomo listener and API must remain loopback-only and match subscription.apiAddress.";
      }
      {
        assertion = !(cfg.settings ? outbounds);
        message = "Use services.sb.staticOutbounds instead of settings.outbounds.";
      }
      {
        assertion =
          builtins.length (builtins.attrNames cfg.backends) < 2
          || (
            lib.isList singBoxInbounds
            && lib.any (
              inbound:
              (inbound.type or "") == "mixed"
              && (inbound.listen or "") == "127.0.0.1"
              && (inbound.listen_port or 0) == 1080
            ) singBoxInbounds
            && lib.all (inbound: (inbound.type or "") != "tun") singBoxInbounds
          );
        message = "Switchable backends require a shared loopback mixed listener on port 1080 and no sing-box TUN inbound.";
      }
      {
        assertion = sub.enable -> cfg.legacyOutboundsFile == null;
        message = "legacyOutboundsFile cannot be used with subscriptions.";
      }
      {
        assertion = cfg.refreshUser == null || sub.enable;
        message = "services.sb.refreshUser requires subscription.enable.";
      }
      {
        assertion =
          (sub.enable || builtins.length (builtins.attrNames cfg.backends) > 1)
          -> lib.hasPrefix "127.0.0.1:" sub.apiAddress;
        message = "The Clash API must be loopback-only.";
      }
      {
        assertion =
          (sub.enable || builtins.length (builtins.attrNames cfg.backends) > 1)
          -> (
            lib.attrByPath [ "experimental" "clash_api" "external_controller" ] null (
              lib.recursiveUpdate { experimental.clash_api.external_controller = sub.apiAddress; } cfg.settings
            ) == sub.apiAddress
          );
        message = "Clash API address must match subscription.apiAddress.";
      }
      {
        assertion = lib.all (s: builtins.match "[A-Za-z0-9_-]+" s.name != null) namedSources;
        message = "Subscription source names must match [A-Za-z0-9_-]+.";
      }
      {
        assertion = lib.unique (map (s: s.name) namedSources) == map (s: s.name) namedSources;
        message = "Subscription source names must be unique.";
      }
    ];
    environment.systemPackages = builtins.attrValues cfg.backends ++ [ sb ];
    environment.etc."sb/config.json".source = settings;
    _module.args.sbModule = {
      inherit
        sb
        update
        runner
        settings
        ;
    };
  };
}
