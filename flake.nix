{
  description = "Subscription management and proxy client control";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      eachSystem = nixpkgs.lib.genAttrs systems;
      package =
        pkgs:
        pkgs.buildGoModule {
          pname = "ph";
          version = "0.1.0";
          src = self;
          vendorHash = "sha256-m8p79jpffBdH81rabn6HFGFINeCqegeYVpC8JX6Dwvg=";
          subPackages = [ "cmd/ph" ];
          checkPhase = ''
            runHook preCheck
            go test ./...
            runHook postCheck
          '';
          meta = {
            description = "Subscription management and proxy client control";
            mainProgram = "ph";
            platforms = systems;
          };
        };
    in
    {
      overlays.default = final: _: { ph = package final; };
      nixosModules.default = { ... }: {
        imports = [ ./nix/modules/nixos.nix ];
        _module.args.phFlake = self;
      };
      nixosModules.mihomo = { ... }: {
        imports = [ ./nix/modules/mihomo.nix ];
        _module.args.phFlake = self;
      };
      darwinModules.default = { ... }: {
        imports = [ ./nix/modules/darwin.nix ];
        _module.args.phFlake = self;
      };
      packages = eachSystem (
        system:
        let
          ph = package nixpkgs.legacyPackages.${system};
        in
        {
          inherit ph;
          default = ph;
        }
      );
      devShells = eachSystem (system: {
        default = nixpkgs.legacyPackages.${system}.mkShell {
          packages = with nixpkgs.legacyPackages.${system}; [
            go
            golangci-lint
            sing-box
          ];
        };
      });
    };
}
