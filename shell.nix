{ pkgs ? import <nixpkgs> { } }:

let
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
