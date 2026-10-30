# The zoekt commands, as shipped in the Docker image (./docker.nix).
{
  lib,
  buildGo127Module,
  version,
}:
buildGo127Module {
  pname = "zoekt";
  inherit version;

  src = ./.;

  # Hash of the Go module dependencies. Update it whenever go.mod or go.sum
  # change: set it to lib.fakeHash, run `nix build .#zoekt` and copy the hash
  # from the error.
  vendorHash = "sha256-Vm5Mpzm7i6v1LxtnwkWbkCa09JlVGKnyIwXD6unqLXc=";

  subPackages = [ "cmd/..." ];

  env.CGO_ENABLED = 0;
  ldflags = [ "-X github.com/sourcegraph/zoekt/index.Version=${version}" ];

  # CI runs the tests outside Nix.
  doCheck = false;

  meta = {
    description = "Fast trigram based code search";
    homepage = "https://github.com/sourcegraph/zoekt";
    license = lib.licenses.asl20;
  };
}
