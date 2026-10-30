# The container image published to ghcr.io/sourcegraph/zoekt, for the
# architecture of the system building it. `nix build .#docker-image` writes a
# tarball for `docker load`.
{
  dockerTools,
  zoekt,
  gitMinimal,
  universal-ctags,
  tini,
  busybox,
  dnsutils,
  # ISO 8601 commit time and git revision, for the image metadata.
  created,
  revision,
}:
let
  # The uid and gid that alpine's `adduser -S` gave the zoekt user when the
  # image was built from a Dockerfile, so existing volumes stay writable.
  uid = "100";
  gid = "101";
in
dockerTools.buildLayeredImage {
  name = "zoekt";
  tag = "latest";
  inherit created;
  # An uncompressed tarball, which podman can read when the publish workflow
  # combines the architectures. The layers are compressed when pushed.
  compressor = "none";

  contents = [
    zoekt
    gitMinimal
    universal-ctags
    tini
    # A shell and core utilities for `docker exec` and scripts.
    busybox
    # dig, host and nslookup for debugging.
    dnsutils
    dockerTools.caCertificates
    dockerTools.usrBinEnv
    (dockerTools.fakeNss.override {
      extraPasswdLines = [ "zoekt:x:${uid}:${gid}::/home/zoekt:/bin/sh" ];
      extraGroupLines = [ "zoekt:x:${gid}:" ];
    })
  ];

  fakeRootCommands = ''
    mkdir -p data/index home/zoekt tmp usr/local
    chmod 1777 tmp
    chown -R ${uid}:${gid} data home/zoekt
    # The Dockerfile installed the zoekt commands here.
    ln -s /bin usr/local/bin
  '';

  config = {
    Entrypoint = [
      "${tini}/bin/tini"
      "--"
    ];
    Cmd = [
      "zoekt-webserver"
      "-index"
      "/data/index"
      "-pprof"
      "-rpc"
    ];
    User = "zoekt";
    WorkingDir = "/home/zoekt";
    Env = [
      "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
      "DATA_DIR=/data/index"
    ];
    Labels = {
      "org.opencontainers.image.title" = "zoekt";
      "org.opencontainers.image.description" = zoekt.meta.description;
      "org.opencontainers.image.source" = "https://github.com/sourcegraph/zoekt";
      "org.opencontainers.image.url" = "https://github.com/sourcegraph/zoekt";
      "org.opencontainers.image.licenses" = "Apache-2.0";
      "org.opencontainers.image.version" = zoekt.version;
      "org.opencontainers.image.revision" = revision;
      "org.opencontainers.image.created" = created;
    };
  };
}
