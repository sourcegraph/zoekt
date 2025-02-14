{ pkgs ? import <nixpkgs> {
  overlays = [
    (import ./ctag-overlay.nix)
  ];
}}:
let
  # pkgs.universal-ctags installs the binary as "ctags", not "universal-ctags"
  # like zoekt expects.
  universal-ctags = pkgs.writeScriptBin "universal-ctags" ''
    #!${pkgs.stdenv.shell}
    exec ${pkgs.universal-ctags}/bin/ctags "$@"
  '';
in
pkgs.mkShell {
  name = "zoekt";

  nativeBuildInputs = [
    pkgs.go_1_23

    # zoekt-git-index
    pkgs.git

    # Used to index symbols
    universal-ctags
  ];
}
