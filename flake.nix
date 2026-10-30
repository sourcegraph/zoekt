{
  description = "The Zoekt developer environment Nix Flake";

  inputs = {
    # "safer" nixpkgs mirror https://determinate.systems/blog/nixpkgs-cooldown/
    nixpkgs.url = "https://flakehub.com/f/DeterminateSystems/nixpkgs-weekly/0.1";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems =
        f:
        nixpkgs.lib.genAttrs systems (
          system:
          f (
            import nixpkgs {
              inherit system;
              overlays = [ self.overlays.ctags ];
            }
          )
        );

      # The pseudo-version go.mod would use for this commit, so image and
      # module versions line up.
      version =
        if self ? rev then "0.0.0-${self.lastModifiedDate}-${builtins.substring 0 12 self.rev}" else "dev";
      # The commit time in ISO 8601, for the image metadata.
      created =
        let
          d = i: n: builtins.substring i n self.lastModifiedDate;
        in
        "${d 0 4}-${d 4 2}-${d 6 2}T${d 8 2}:${d 10 2}:${d 12 2}Z";
    in
    {
      formatter = forAllSystems (pkgs: pkgs.nixfmt-tree);

      devShells = forAllSystems (pkgs: {
        default = import ./shell.nix { inherit pkgs; };
      });

      packages = forAllSystems (
        pkgs:
        let
          zoekt = pkgs.callPackage ./zoekt.nix { inherit version; };
        in
        {
          # The dev shell's tools as one directory, for environments that put
          # its bin on PATH instead of entering `nix develop`: orbs
          # (.agents/setup) and CI. Unlike the dev shell, it doesn't pull in a C
          # toolchain.
          dev-tools = pkgs.buildEnv {
            name = "zoekt-dev-tools";
            paths = (import ./shell.nix { inherit pkgs; }).nativeBuildInputs;
          };

          inherit zoekt;
        }
        // nixpkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          docker-image = pkgs.callPackage ./docker.nix {
            inherit zoekt created;
            revision = self.rev or self.dirtyRev;
          };
        }
      );

      overlays.ctags = import ./ctag-overlay.nix;
    };
}
