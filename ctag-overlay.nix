# Pins universal-ctags to the nightly build that ./install-ctags-alpine.sh
# installs in the Docker image, so Docker, CI, orbs and `nix develop` share one
# pin. Bump ctags by editing that script; this overlay reads its CTAGS_* lines.
#
# Like nixpkgs' package, it also provides "universal-ctags", the name zoekt
# looks up on PATH.
final: prev:
let
  inherit (final) lib;

  pins = lib.listToAttrs (
    lib.concatMap (
      line:
      let
        m = builtins.match "(CTAGS_[A-Z0-9_]+)=([^$]*)" line;
      in
      lib.optional (m != null) (lib.nameValuePair (lib.elemAt m 0) (lib.elemAt m 1))
    ) (lib.splitString "\n" (builtins.readFile ./install-ctags-alpine.sh))
  );
  version = pins.CTAGS_VERSION;
  commit = pins.CTAGS_COMMIT;

  # The nightly builds publish static Linux binaries, the same ones the Docker
  # image uses.
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
          sha256 = pins."CTAGS_TAR_SHA256_${lib.toUpper linuxArch}";
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
