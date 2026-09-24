{
  config,
  lib,
  pkgs,
  sbFlake,
  ...
}:
let
  cfg = config.services.sbMihomo;
  json = pkgs.formats.json { };
  state = "/var/lib/sb-mihomo";
  template = json.generate "sb-mihomo-template.json" (
    {
      mixed-port = 2080;
      bind-address = "127.0.0.1";
      allow-lan = false;
      external-controller = "127.0.0.1:9090";
    }
    // cfg.settings
  );
  sources = json.generate "sb-mihomo-sources.json" {
    subscriptions = map (source: {
      id = source.name;
      url_file = source.urlFile;
      prefix = source.prefix;
      auto = source.autoSelect;
      include_tags = source.includeTags;
      exclude_tags = source.excludeTags;
      include_servers = source.includeServers;
      exclude_servers = source.excludeServers;
    }) cfg.sources;
  };
  cli = pkgs.sb or sbFlake.packages.${pkgs.stdenv.hostPlatform.system}.sb;
  settings = json.generate "sb-mihomo-config.json" {
    backend = "mihomo";
    mihomo = "${cfg.package}/bin/mihomo";
    template_file = toString template;
    state_dir = state;
    api_url = "http://127.0.0.1:9090";
    overrides_file = "${state}/overrides.json";
    stores = [
      {
        name = "nix";
        path = toString sources;
        writable = false;
      }
      {
        name = "local";
        path = "${state}/subscriptions.json";
        writable = true;
      }
    ];
    restart_command = [
      "${pkgs.systemd}/bin/systemctl"
      "restart"
      "sb-mihomo.service"
    ];
  };
  sb = pkgs.writeShellScriptBin "sb-mihomo" ''
    exec ${cli}/bin/sb --config ${settings} "$@"
  '';
  runner = pkgs.writeShellScript "sb-mihomo-run" ''
    set -eu
    umask 077
    ${sb}/bin/sb-mihomo prepare
    exec ${cfg.package}/bin/mihomo -f ${state}/config.json -d ${state}
  '';
in
{
  options.services.sbMihomo = {
    enable = lib.mkEnableOption "mihomo managed by sb (loopback mixed proxy, no TUN)";
    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.mihomo;
    };
    sources = lib.mkOption {
      type = lib.types.listOf (
        lib.types.submodule {
          options = {
            name = lib.mkOption { type = lib.types.str; };
            urlFile = lib.mkOption { type = lib.types.str; };
            prefix = lib.mkOption {
              type = lib.types.str;
              default = "";
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
        }
      );
      default = [ ];
    };
    settings = lib.mkOption {
      type = lib.types.submodule { freeformType = json.type; };
      default = { };
      description = "Native mihomo template settings (not sing-box settings).";
    };
    updateInterval = lib.mkOption {
      type = lib.types.ints.positive;
      default = 86400;
    };
  };
  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = !(lib.attrByPath [ "services" "sb" "enable" ] false config);
        message = "services.sbMihomo and services.sb cannot share the default proxy/API ports.";
      }
      {
        assertion =
          !(
            cfg.settings ? proxies
            || cfg.settings ? proxy-groups
            || cfg.settings ? proxy-providers
            || cfg.settings ? rules
            || cfg.settings ? mode
            || cfg.settings ? tun
          );
        message = "sb manages mihomo proxies, proxy-groups and rules; native providers, mode and TUN are unsupported.";
      }
      {
        assertion =
          lib.attrByPath [ "external-controller" ] "127.0.0.1:9090" cfg.settings == "127.0.0.1:9090"
          && lib.attrByPath [ "allow-lan" ] false cfg.settings == false
          && lib.attrByPath [ "bind-address" ] "127.0.0.1" cfg.settings == "127.0.0.1";
        message = "The managed mihomo listener and API must remain loopback-only.";
      }
      {
        assertion = lib.all (s: builtins.match "[A-Za-z0-9_-]+" s.name != null) cfg.sources;
        message = "Mihomo source names must match [A-Za-z0-9_-]+.";
      }
      {
        assertion = lib.unique (map (s: s.name) cfg.sources) == map (s: s.name) cfg.sources;
        message = "Mihomo source names must be unique.";
      }
    ];
    environment.systemPackages = [
      cfg.package
      sb
    ];
    systemd.services.sb-mihomo = {
      description = "mihomo managed by sb";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        ExecStart = runner;
        Restart = "on-failure";
        User = "root";
        StateDirectory = "sb-mihomo";
      };
    };
    systemd.services.sb-mihomo-update = {
      description = "Update sb mihomo subscriptions";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        Type = "oneshot";
        User = "root";
        ExecStart = pkgs.writeShellScript "sb-mihomo-update" ''
          exec ${sb}/bin/sb-mihomo update
        '';
      };
    };
    systemd.timers.sb-mihomo-update = {
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "5m";
        OnUnitActiveSec = "${toString cfg.updateInterval}s";
        Unit = "sb-mihomo-update.service";
      };
    };
  };
}
