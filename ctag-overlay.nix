# Pins universal-ctags for the Docker image, CI, orbs and `nix develop`.
#
# When bumping, update version, commit and the sha256 sums together from the
# release page/API:
# https://github.com/universal-ctags/ctags-nightly-build/releases
#
# Like nixpkgs' package, it also provides "universal-ctags", the name zoekt
# looks up on PATH.
final: prev:
let
  version = "2024.01.07";
  commit = "4053f69a35d8d3d307b274040f27c147eec79ee7";
  # GitHub release asset digests for uctags-${version}-linux-*.tar.xz
  sha256 = {
    x86_64 = "0c0bbc9f81d3f7151988b94e78c64914ab41ee4c5e10debfe79f73fee54a68a0";
    aarch64 = "a50e25cb5b4ced8fea119984695e77464aaa73d5d4a53c10fec34dd82b1d9e5f";
  };

  # The nightly builds publish static Linux binaries.
  linuxArch =
    {
      x86_64-linux = "x86_64";
      aarch64-linux = "aarch64";
    }
    .${final.stdenv.hostPlatform.system} or null;
in
{
  universal-ctags =
    if linuxArch != null then
      final.stdenvNoCC.mkDerivation {
        pname = "universal-ctags";
        inherit version;
        src = final.fetchurl {
          url = "https://github.com/universal-ctags/ctags-nightly-build/releases/download/${version}%2B${commit}/uctags-${version}-linux-${linuxArch}.tar.xz";
          sha256 = sha256.${linuxArch};
        };
        installPhase = ''
          install -Dm755 bin/ctags $out/bin/ctags
          ln -s ctags $out/bin/universal-ctags
        '';
      }
    else
      # Elsewhere (macOS), build the same commit from source.
      prev.universal-ctags.overrideAttrs {
        inherit version;
        src = builtins.fetchGit {
          url = "https://github.com/universal-ctags/ctags";
          rev = commit;
          shallow = true;
        };
        # Else we get `make[1]: *** No rule to make target 'optlib/cmake.c'.  Stop.`
        doCheck = false;
        checkFlags = [ ];
      };
}
