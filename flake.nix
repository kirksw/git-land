{
  description = "land - repository-aware landing workflow CLI";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      # Tag checkouts report their tag (v0.1); every other checkout falls back
      # to the short revision, so land --version always identifies the source.
      version =
        let
          ref = self.sourceInfo.ref or null;
        in
        if ref != null && builtins.match "refs/tags/v.*" ref != null then
          nixpkgs.lib.removePrefix "refs/tags/v" ref
        else
          "dev-" + (self.shortRev or self.dirtyShortRev or "unknown");
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        rec {
          land = pkgs.buildGoModule {
            pname = "land";
            inherit version;
            src = self;
            # Dependencies are vendored in the repository, so no module hash
            # is needed and builds are hermetic without a module proxy.
            vendorHash = null;
            subPackages = [ "cmd/land" ];
            ldflags = [
              "-s"
              "-w"
              "-X main.version=${version}"
            ];
            nativeBuildInputs = [ pkgs.git ];
            env.GOWORK = "off";
            meta = {
              description = "Repository-aware landing workflow CLI for people and coding agents";
              homepage = "https://github.com/kirksw/git-land";
              license = nixpkgs.lib.licenses.asl20;
              mainProgram = "land";
              platforms = systems;
            };
          };
          default = land;
        }
      );

      apps = forAllSystems (system: {
        land = {
          type = "app";
          program = "${self.packages.${system}.land}/bin/land";
        };
        default = self.apps.${system}.land;
      });
    };
}
