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
        "https://github.com/NixOS/nixpkgs/archive/a5d12145047970d663764ce95bf1e459e734a014.tar.gz";
      sha256 = "1rbnmbc3mvk5in86wx6hla54d7kkmgn6pz0zwigcchhmi2dv76h3";
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
    pkgs.go

    # zoekt-git-index
    pkgs.git

    # Used to index symbols
    ctagsWrapper
    pkgs.universal-ctags
  ];
}
