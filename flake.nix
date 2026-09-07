{
  description = "Subscription management and sing-box control";
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
          pname = "sb";
          version = "0.1.0";
          src = self;
          vendorHash = null;
          subPackages = [ "cmd/sb" ];
          checkPhase = ''
            runHook preCheck
            go test ./...
            runHook postCheck
          '';
          meta = {
            description = "Subscription management and sing-box control";
            mainProgram = "sb";
            platforms = systems;
          };
        };
    in
    {
      overlays.default = final: prev: { sb = package final; };
      packages = eachSystem (
        system:
        let
          sb = package nixpkgs.legacyPackages.${system};
        in
        {
          inherit sb;
          default = sb;
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
