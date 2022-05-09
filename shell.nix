let
  # Pin a specific version of universal-ctags to the same version as in cmd/symbols/ctags-install-alpine.sh.
  ctags-overlay = (self: super: {
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
  });
  # Pin a specific version of nixpkgs to ensure we get the same packages.
 pkgs = import
    (fetchTarball {
      url =
        "https://github.com/NixOS/nixpkgs/archive/cbe587c735b734405f56803e267820ee1559e6c1.tar.gz";
      sha256 = "0jii8slqbwbvrngf9911z3al1s80v7kk8idma9p9k0d5fm3g4z7h";
    })
    { overlays = [ ctags-overlay ]; };
  # pkgs.universal-ctags installs the binary as "ctags", not "universal-ctags"
  # like zoekt expects.
  ctagsWrapper = pkgs.writeScriptBin "universal-ctags" ''
    #!${pkgs.stdenv.shell}
    exec ${pkgs.universal-ctags}/bin/ctags "$@"
  '';

in pkgs.mkShell {
  name = "zoekt";

  nativeBuildInputs = [
    pkgs.go_1_18

    # zoekt-git-index
    pkgs.git

    # Used to index symbols
    ctagsWrapper
    pkgs.universal-ctags
  ];
}
