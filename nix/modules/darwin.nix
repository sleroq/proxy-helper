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
  log = "/var/log/sing-box.log";
in
{
  imports = [ ./common.nix ];
  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.routingMark == 0;
        message = "services.sb.routingMark is Linux-only.";
      }
      {
        assertion = cfg.extraCapabilities == [ ];
        message = "services.sb.extraCapabilities is Linux-only.";
      }
    ];
    system.activationScripts.preActivation.text = lib.mkIf (sub.enable && cfg.refreshUser != null) ''
      ${pkgs.coreutils}/bin/install -d -o root -g wheel -m 0755 ${lib.escapeShellArg cfg.stateDir}
      ${pkgs.coreutils}/bin/install -o ${lib.escapeShellArg cfg.refreshUser} -g wheel -m 0600 /dev/null ${lib.escapeShellArg "${cfg.stateDir}/refresh-request"}
    '';
    launchd.daemons.sing-box.serviceConfig = {
      ProgramArguments = [ "${sbModule.runner}" ];
      RunAtLoad = true;
      KeepAlive = true;
      StandardOutPath = log;
      StandardErrorPath = log;
    };
    launchd.daemons.sing-box-update = lib.mkIf sub.enable {
      serviceConfig = {
        ProgramArguments = [ "${sbModule.update}" ];
        StartInterval = sub.updateInterval;
        StandardOutPath = log;
        StandardErrorPath = log;
      };
    };
    launchd.daemons.sing-box-refresh = lib.mkIf (sub.enable && cfg.refreshUser != null) {
      serviceConfig = {
        ProgramArguments = [ "${sbModule.update}" ];
        WatchPaths = [ "${cfg.stateDir}/refresh-request" ];
        StandardOutPath = log;
        StandardErrorPath = log;
      };
    };
  };
}
