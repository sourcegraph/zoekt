{
  pkgs ? import <nixpkgs> {
    overlays = [
      (import ./ctag-overlay.nix)
    ];
  },
}:
pkgs.mkShell {
  name = "zoekt";

  # Also installed by .agents/setup (orbs) and CI via the flake's dev-tools
  # package.
  packages = [
    # go.mod's toolchain line makes this Go download and switch to the exact
    # toolchain zoekt uses.
    pkgs.go_1_27

    # zoekt-git-index and tests
    pkgs.gitMinimal

    # Used to index symbols, pinned by ./ctag-overlay.nix
    pkgs.universal-ctags

    # Protobuf lint, format and generate checks
    pkgs.buf
  ];
}
