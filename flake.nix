{
  description = "The Zoekt developer environment Nix Flake";

  inputs = { nixpkgs.url = "nixpkgs/nixos-unstable"; };

  outputs = { self, nixpkgs }: {
    devShells = nixpkgs.lib.genAttrs [
      "x86_64-linux"
      "aarch64-linux"
      "aarch64-darwin"
      "x86_64-darwin"
    ] (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ self.overlays.ctags ];
        };
      in { default = import ./shell.nix { inherit pkgs; }; });
    # Pin a specific version of universal-ctags to the same version as in ./install-ctags-alpine.sh.
    overlays.ctags = import ./ctag-overlay.nix;
  };
}
