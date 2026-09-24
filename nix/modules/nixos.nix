{
  config,
  lib,
  pkgs,
  sbModule,
  ...
}:
let
  cfg = config.services.sb;
  sub = cfg.subscription;
  caps = [
    "CAP_NET_ADMIN"
    "CAP_NET_RAW"
  ]
  ++ cfg.extraCapabilities;
  rule = "priority 8999 fwmark ${toString cfg.routingMark} lookup main";
in
{
  imports = [ ./common.nix ];
  config = lib.mkIf cfg.enable {
    systemd.services.sing-box = {
      description = "sing-box";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        ExecStart = sbModule.runner;
        Restart = "on-failure";
        User = "root";
        Group = "root";
        StateDirectory = "sing-box";
        AmbientCapabilities = caps;
        CapabilityBoundingSet = caps;
      }
      // lib.optionalAttrs (cfg.routingMark != 0) {
        ExecStartPre = pkgs.writeShellScript "sb-routing-rule-start" ''
          ${pkgs.iproute2}/bin/ip rule del ${rule} 2>/dev/null || true
          ${pkgs.iproute2}/bin/ip rule add ${rule}
        '';
        ExecStopPost = pkgs.writeShellScript "sb-routing-rule-stop" ''
          ${pkgs.iproute2}/bin/ip rule del ${rule} 2>/dev/null || true
        '';
      };
    };
    systemd.services.sing-box-update = lib.mkIf sub.enable {
      description = "Update sing-box subscription";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        Type = "oneshot";
        User = "root";
        ExecStart = sbModule.update;
      };
    };
    systemd.timers.sing-box-update = lib.mkIf sub.enable {
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "5m";
        OnUnitActiveSec = "${toString sub.updateInterval}s";
        Unit = "sing-box-update.service";
      };
    };
    security.polkit.enable = lib.mkIf (cfg.refreshUser != null && sub.enable) (lib.mkDefault true);
    security.polkit.extraConfig = lib.mkIf (cfg.refreshUser != null && sub.enable) ''
      polkit.addRule(function(action, subject) {
        if (subject.user == ${builtins.toJSON cfg.refreshUser} &&
            action.id == "org.freedesktop.systemd1.manage-units" &&
            action.lookup("unit") == "sing-box-update.service" &&
            action.lookup("verb") == "start") return polkit.Result.YES;
      });
    '';
  };
}
