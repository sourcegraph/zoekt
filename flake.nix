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
        "x86_64-darwin"
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
      # Pin a specific version of universal-ctags to the same version as in ./install-ctags-alpine.sh.
      overlays.ctags = import ./ctag-overlay.nix;
    };
}
