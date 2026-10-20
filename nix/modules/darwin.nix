{
  config,
  lib,
  pkgs,
  phModule,
  ...
}:
let
  cfg = config.services.ph;
  sub = cfg.subscription;
  log = "/var/log/ph-proxy.log";
in
{
  imports = [ ./common.nix ];
  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.routingMark == 0;
        message = "services.ph.routingMark is Linux-only.";
      }
      {
        assertion = cfg.extraCapabilities == [ ];
        message = "services.ph.extraCapabilities is Linux-only.";
      }
    ];
    system.activationScripts.preActivation.text = lib.mkIf (sub.enable && cfg.refreshUser != null) ''
      ${pkgs.coreutils}/bin/install -d -o root -g wheel -m 0755 ${lib.escapeShellArg cfg.stateDir}
      ${pkgs.coreutils}/bin/install -o ${lib.escapeShellArg cfg.refreshUser} -g wheel -m 0600 /dev/null ${lib.escapeShellArg "${cfg.stateDir}/refresh-request"}
    '';
    launchd.daemons = {
      ph-proxy.serviceConfig = {
        ProgramArguments = [ "${phModule.runner}" ];
        RunAtLoad = true;
        KeepAlive = true;
        StandardOutPath = log;
        StandardErrorPath = log;
      };
      ph-proxy-update = lib.mkIf sub.enable {
        serviceConfig = {
          ProgramArguments = [ "${phModule.update}" ];
          StartInterval = sub.updateInterval;
          StandardOutPath = log;
          StandardErrorPath = log;
        };
      };
      ph-proxy-refresh = lib.mkIf (sub.enable && cfg.refreshUser != null) {
        serviceConfig = {
          ProgramArguments = [ "${phModule.update}" ];
          WatchPaths = [ "${cfg.stateDir}/refresh-request" ];
          StandardOutPath = log;
          StandardErrorPath = log;
        };
      };
    };
  };
}
