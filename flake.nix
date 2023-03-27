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
    # Pin a specific version of universal-ctags to the same version as in cmd/symbols/ctags-install-alpine.sh.
    overlays.ctags = self: super: rec {
      universal-ctags = super.universal-ctags.overrideAttrs (old: {
        version = "5.9.20220403.0";
        src = super.fetchFromGitHub {
          owner = "universal-ctags";
          repo = "ctags";
          rev = "f95bb3497f53748c2b6afc7f298cff218103ab90";
          sha256 = "sha256-pd89KERQj6K11Nue3YFNO+NLOJGqcMnHkeqtWvMFk38=";
        };
        # disable checks, else we get `make[1]: *** No rule to make target 'optlib/cmake.c'.  Stop.`
        doCheck = false;
        checkFlags = [ ];
      });
    };
  };
}
