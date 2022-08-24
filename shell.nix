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
        "https://github.com/NixOS/nixpkgs/archive/6f38b43c8c84c800f93465b2241156419fd4fd52.tar.gz";
      sha256 = "0xw3y3jx1bcnwsc0imacbp5m8f51b66s9h8kk8qnfbckwv67dhgd";
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
    pkgs.go_1_19

    # zoekt-git-index
    pkgs.git

    # Used to index symbols
    ctagsWrapper
    pkgs.universal-ctags
  ];
}
