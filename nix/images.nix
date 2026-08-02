{
  pkgs,
  package,
  version,
}:

let
  # Both images run as a non-root user that owns nothing. Nothing in either
  # image needs to write to its own filesystem: the web tier reads a database
  # and the ingest job writes to a mounted cache directory.
  users = pkgs.runCommand "image-users" { } ''
    mkdir -p $out/etc
    echo 'app:x:1000:1000:app:/tmp:/noshell' > $out/etc/passwd
    echo 'app:x:1000:' > $out/etc/group
  '';
in
{
  # The web image is the binary, CA certificates, and a passwd entry. No shell,
  # no package manager, no distribution userland, so most CVE traffic against it
  # is about software that is not there.
  web = pkgs.dockerTools.buildLayeredImage {
    name = "sourceslist-web";
    tag = version;
    contents = [
      package
      pkgs.cacert
      users
    ];
    config = {
      Entrypoint = [ "${package}/bin/web" ];
      User = "1000:1000";
      ExposedPorts."8080/tcp" = { };
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        "LISTEN_ADDRESS=0.0.0.0:8080"
      ];
    };
  };

  # The ingest image additionally carries the Postgres client tools, because the
  # extract step shells out to pg_restore, createdb, dropdb, and psql. That is a
  # deliberate cost: reimplementing the custom dump format in Go to keep the
  # image small would be a large amount of fragile code to save a few megabytes.
  ingest = pkgs.dockerTools.buildLayeredImage {
    name = "sourceslist-ingest";
    tag = version;
    contents = [
      package
      pkgs.cacert
      pkgs.postgresql
      users
    ];
    config = {
      Entrypoint = [ "${package}/bin/ingest" ];
      User = "1000:1000";
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        "UDD_DUMP_URL=https://udd.debian.org/dumps/udd.dump"
        "UDD_CACHE_DIR=/var/cache/sourceslist"
        "UDD_SCRATCH_DATABASE=udd_scratch"
      ];
      Volumes."/var/cache/sourceslist" = { };
    };
  };
}
