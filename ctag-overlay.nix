# Pins universal-ctags to the release that ./install-ctags-alpine.sh builds for
# the Docker image, so Docker, CI, orbs and `nix develop` share one pin. Bump
# ctags by editing that script; this overlay reads its CTAGS_* lines.
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
in
{
  universal-ctags =
    # Use nixpkgs' cached build when it has the pinned version, instead of
    # compiling ctags (about 2 minutes).
    if prev.universal-ctags.version == version then
      prev.universal-ctags
    else
      prev.universal-ctags.overrideAttrs {
        inherit version;
        src = final.fetchurl {
          url = "https://github.com/universal-ctags/ctags/releases/download/v${version}/universal-ctags-${version}.tar.gz";
          sha256 = pins.CTAGS_TAR_SHA256;
        };
        # Else we get `make[1]: *** No rule to make target 'optlib/cmake.c'.  Stop.`
        doCheck = false;
        checkFlags = [ ];
      };
}
