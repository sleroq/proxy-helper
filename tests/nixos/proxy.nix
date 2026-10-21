{ self, pkgs }:
let
  lib = pkgs.lib;
  fixtureAddress = "192.0.2.2";
  subscriptionURL = "http://${fixtureAddress}:18080/subscription";
  routing = builtins.fromJSON (builtins.readFile ../routing/sing-box.json);
  serverConfig = pkgs.formats.json { };
  shadowsocks = serverConfig.generate "shadowsocks-server.json" {
    inbounds = [
      {
        type = "shadowsocks";
        listen = "0.0.0.0";
        listen_port = 18388;
        method = "aes-128-gcm";
        password = "fixture-secret";
      }
    ];
    outbounds = [ { type = "direct"; } ];
  };
  client = address: {
    imports = [ self.nixosModules.default ];
    networking.interfaces.eth1.ipv4.addresses = lib.mkForce [
      {
        inherit address;
        prefixLength = 24;
      }
    ];
    environment.systemPackages = [
      pkgs.curl
      pkgs.bind.dnsutils
    ];
    environment.etc."ph/provider-url" = {
      text = subscriptionURL + "\n";
      mode = "0600";
    };
    # Refresh explicitly in the test, not while another node's assertions run.
    systemd.timers.ph-proxy-update.timerConfig.OnBootSec = lib.mkForce "1h";
    services.ph = {
      enable = true;
      subscription = {
        enable = true;
        testURL = "http://${fixtureAddress}:18080/health";
        sources = [
          {
            name = "fixture";
            urlFile = "/etc/ph/provider-url";
          }
        ];
      };
    };
  };
in
pkgs.testers.runNixOSTest {
  name = "ph-subscription-proxy-routing";
  nodes = {
    server = {
      networking.firewall.enable = false;
      networking.interfaces = {
        eth1.ipv4.addresses = lib.mkForce [
          {
            address = fixtureAddress;
            prefixLength = 24;
          }
        ];
        lo.ipv4.addresses = [
          {
            address = "198.18.0.1";
            prefixLength = 32;
          }
        ];
      };
      # No router: the target is reachable only by sockets opened on this VM.
      boot.kernel.sysctl."net.ipv4.ip_forward" = 0;
      services.dnsmasq = {
        enable = true;
        settings = {
          no-resolv = true;
          no-hosts = true;
          bind-interfaces = true;
          listen-address = "198.18.0.1";
          address = "/routed.test/198.18.0.1";
          local-ttl = 0;
          log-queries = true;
        };
      };
      systemd.services = {
        dnsmasq = {
          requires = [ "network-addresses-lo.service" ];
          after = [ "network-addresses-lo.service" ];
        };
        subscription-fixture = {
          wantedBy = [ "multi-user.target" ];
          after = [ "network.target" ];
          serviceConfig.ExecStart = "${pkgs.python3}/bin/python3 ${../distros/fixture.py} --listen 0.0.0.0 --server ${fixtureAddress}";
        };
        shadowsocks = {
          wantedBy = [ "multi-user.target" ];
          after = [ "network.target" ];
          serviceConfig.ExecStart = "${pkgs.sing-box}/bin/sing-box run -c ${shadowsocks}";
        };
      };
    };
    mixed = lib.recursiveUpdate (client "192.0.2.3") {
      services.ph.backends = {
        "sing-box" = pkgs.sing-box;
        mihomo = pkgs.mihomo;
      };
    };
    tun = lib.recursiveUpdate (client "192.0.2.4") {
      # The native TCP stack accepts redirected connections on this interface.
      networking.firewall.trustedInterfaces = [ "ph-tun" ];
      networking.nameservers = [ "203.0.113.53" ];
      networking.dhcpcd.extraConfig = "nohook resolv.conf";
      services.resolved.enable = false;
      services.ph.settings = routing;
    };
    standalone = {
      imports = [ self.nixosModules.mihomo ];
      networking.interfaces.eth1.ipv4.addresses = lib.mkForce [
        {
          address = "192.0.2.5";
          prefixLength = 24;
        }
      ];
      environment.systemPackages = [ pkgs.curl ];
      environment.etc."ph/provider-url" = {
        text = subscriptionURL + "\n";
        mode = "0600";
      };
      systemd.timers.ph-mihomo-update.timerConfig.OnBootSec = lib.mkForce "1h";
      services.phMihomo = {
        enable = true;
        sources = [
          {
            name = "fixture";
            urlFile = "/etc/ph/provider-url";
          }
        ];
      };
    };
  };
  testScript = ''
    import json

    start_all()
    server.wait_for_unit("subscription-fixture.service")
    server.wait_for_unit("shadowsocks.service")
    server.wait_for_unit("dnsmasq.service")
    server.wait_until_succeeds("systemctl is-active network-addresses-eth1.service", timeout=30)
    server.wait_for_open_port(18080)
    server.wait_for_open_port(18388)

    def proxy_http(port=1080):
        return (
            'test "$(curl -fsS --max-time 5 --noproxy "" '
            f'--proxy http://127.0.0.1:{port} '
            'http://198.18.0.1:18080/health)" = fixture-ok'
        )

    ip_http = "curl -fsS --max-time 3 --noproxy '*' http://198.18.0.1:18080/health"

    with subtest("NixOS subscription, both cores, pin and managed refresh"):
        mixed.wait_until_succeeds("systemctl is-active network-addresses-eth1.service", timeout=30)
        mixed.fail(ip_http)
        mixed.succeed("ph update")
        mixed.wait_for_open_port(9090)
        manifest = json.loads(mixed.succeed("cat /var/lib/sing-box/subscription.json"))
        tag = manifest["nodes"][0]["tag"]
        mixed.succeed(f"ph pin '{tag}'")
        for backend in ["sing-box", "mihomo"]:
            mixed.succeed(f"ph backend select {backend}")
            mixed.wait_for_unit("ph-proxy.service")
            mixed.wait_until_succeeds(proxy_http(), timeout=30)
            mixed.succeed("ph test")
            mixed.succeed("ph refresh")
            mixed.wait_until_succeeds(proxy_http(), timeout=30)
            server.succeed("systemctl stop shadowsocks.service")
            mixed.fail(proxy_http())
            server.succeed("systemctl start shadowsocks.service")
            server.wait_for_open_port(18388)
            mixed.wait_until_succeeds(proxy_http(), timeout=30)
            current = json.loads(mixed.succeed("cat /var/lib/sing-box/subscription.json"))
            assert current["pinnedTag"] == tag and current["pinActive"]
        mixed.succeed("systemctl is-active ph-proxy-update.timer")

    with subtest("Independent NixOS mihomo module"):
        standalone.wait_until_succeeds("systemctl is-active network-addresses-eth1.service", timeout=30)
        standalone.fail(ip_http)
        standalone.succeed("ph-mihomo update")
        standalone.wait_for_unit("ph-mihomo.service")
        standalone.wait_until_succeeds(proxy_http(2080), timeout=30)
        standalone.succeed("systemctl start ph-mihomo-update.service")
        standalone.wait_until_succeeds(proxy_http(2080), timeout=30)
        server.succeed("systemctl stop shadowsocks.service")
        standalone.fail(proxy_http(2080))
        server.succeed("systemctl start shadowsocks.service")
        server.wait_for_open_port(18388)
        standalone.wait_until_succeeds(proxy_http(2080), timeout=30)

    def dns(name, tcp=False):
        transport = "+tcp" if tcp else "+notcp"
        return (
            f"test \"$(dig +short +time=2 +tries=1 {transport} "
            f"@203.0.113.53 {name}.routed.test A)\" = 198.18.0.1"
        )

    def routed():
        tun.wait_until_succeeds("ip route get 198.18.0.1 | grep 'dev ph-tun'", timeout=30)
        tun.wait_until_succeeds(f"test \"$({ip_http})\" = fixture-ok", timeout=30)
        tun.succeed(dns("udp"))
        tun.succeed(dns("tcp", tcp=True))
        tun.succeed(
            "test \"$(curl -fsS --max-time 5 --noproxy '*' "
            "http://system-resolver.routed.test:18080/health)\" = fixture-ok"
        )

    with subtest("No direct path before TUN"):
        tun.wait_until_succeeds("systemctl is-active network-addresses-eth1.service", timeout=30)
        tun.fail(ip_http)
        tun.fail(dns("before"))
        tun.fail(dns("before-tcp", tcp=True))

    with subtest("Subscription to transparent TCP and hijacked UDP/TCP DNS"):
        tun.succeed("sing-box version")
        tun.succeed("ph update")
        tun.wait_for_unit("ph-proxy.service")
        tun.wait_for_open_port(1080)
        routed()

    with subtest("Traffic and uncached DNS depend on the subscription proxy"):
        server.succeed("systemctl stop shadowsocks.service")
        tun.succeed("systemctl is-active ph-proxy.service")
        tun.fail(ip_http)
        tun.fail(dns("outage"))
        tun.fail(dns("outage-tcp", tcp=True))
        server.succeed("systemctl start shadowsocks.service")
        server.wait_for_open_port(18388)
        routed()

    with subtest("TUN routes disappear when the managed service stops"):
        tun.succeed("systemctl stop ph-proxy.service")
        tun.fail("ip link show ph-tun")
        tun.fail(ip_http)
        tun.fail(dns("stopped"))
        tun.succeed("systemctl start ph-proxy.service")
        tun.wait_for_open_port(1080)
        routed()
  '';
}
