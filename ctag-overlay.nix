self: super: rec {
  my-universal-ctags = super.universal-ctags.overrideAttrs (old: rec {
    version = "6.1.0";
    src = super.fetchFromGitHub {
      owner = "universal-ctags";
      repo = "ctags";
      rev = "v${version}";
      sha256 = "sha256-f8+Ifjn7bhSYozOy7kn+zCLdHGrH3iFupHUZEGynz9Y=";
    };
    # disable checks, else we get `make[1]: *** No rule to make target 'optlib/cmake.c'.  Stop.`
    doCheck = false;
    checkFlags = [ ];
  });

  # Skip building if same ctags version as registry
  universal-ctags = if super.universal-ctags.version == my-universal-ctags.version then super.universal-ctags else my-universal-ctags;
}
