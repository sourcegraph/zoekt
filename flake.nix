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
    in
    {
      formatter = forAllSystems (pkgs: pkgs.nixfmt-tree);

      devShells = forAllSystems (pkgs: {
        default = import ./shell.nix { inherit pkgs; };
      });

      # The dev shell's tools as one directory, for environments that put its
      # bin on PATH instead of entering `nix develop`: orbs (.agents/setup) and
      # CI. Unlike the dev shell, it doesn't pull in a C toolchain.
      packages = forAllSystems (pkgs: {
        dev-tools = pkgs.buildEnv {
          name = "zoekt-dev-tools";
          paths = (import ./shell.nix { inherit pkgs; }).nativeBuildInputs;
        };
      });

      # Pin universal-ctags to the version in ./install-ctags-alpine.sh.
      overlays.ctags = import ./ctag-overlay.nix;
    };
}
