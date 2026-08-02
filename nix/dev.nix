{
  pkgs,
  images,
  version,
}:

let
  postgresImage = "docker.io/library/postgres:18-alpine";

  # The compose file is generated rather than checked in, so the image tags it
  # references cannot drift from the images Nix just built. Writing JSON is
  # enough: docker-compose.yaml is YAML, and every YAML parser accepts JSON.
  compose = builtins.toJSON {
    name = "sourceslist";
    services = {
      database = {
        image = postgresImage;
        environment = {
          POSTGRES_USER = "sourceslist";
          POSTGRES_PASSWORD = "devonly";
          POSTGRES_DB = "sourceslist";
        };
        volumes = [ "pgdata:/var/lib/postgresql/data" ];
        # Published on loopback only. The development environment must not be
        # reachable from anything but this machine.
        ports = [ "127.0.0.1:5432:5432" ];
        healthcheck = {
          test = [
            "CMD-SHELL"
            "pg_isready -U sourceslist -d sourceslist"
          ];
          interval = "5s";
          timeout = "5s";
          retries = 10;
        };
      };

      web = {
        image = "sourceslist-web:${version}";
        depends_on.database.condition = "service_healthy";
        environment = {
          DATABASE_URL = "postgres://sourceslist:devonly@database:5432/sourceslist";
          LISTEN_ADDRESS = "0.0.0.0:8080";
        };
        ports = [ "127.0.0.1:8080:8080" ];
      };

      # A job rather than a service: it runs once and exits. The `manual`
      # profile keeps `dev-up` from starting it, because a 2.4 GiB download is
      # not something that should happen because somebody booted the stack.
      ingest = {
        image = "sourceslist-ingest:${version}";
        profiles = [ "manual" ];
        depends_on.database.condition = "service_healthy";
        environment = {
          DATABASE_URL = "postgres://sourceslist:devonly@database:5432/sourceslist";
          UDD_DUMP_URL = "https://udd.debian.org/dumps/udd.dump";
          UDD_CACHE_DIR = "/var/cache/sourceslist";
          UDD_SCRATCH_DATABASE = "udd_scratch";
        };
        volumes = [ "uddcache:/var/cache/sourceslist" ];
      };
    };

    volumes = {
      pgdata = { };
      uddcache = { };
    };
  };

  composeFile = pkgs.writeText "docker-compose.json" compose;

  podmanCompose = "${pkgs.podman}/bin/podman compose --file ${composeFile}";
in
{
  up = pkgs.writeShellApplication {
    name = "dev-up";
    runtimeInputs = [
      pkgs.podman
      pkgs.podman-compose
      pkgs.gzip
    ];
    text = ''
      echo "loading images built by nix"
      ${pkgs.gzip}/bin/gzip -dc ${images.web} | podman load
      ${pkgs.gzip}/bin/gzip -dc ${images.ingest} | podman load

      echo "starting"
      ${podmanCompose} up --detach

      echo
      echo "site:     http://127.0.0.1:8080"
      echo "database: postgres://sourceslist:devonly@127.0.0.1:5432/sourceslist"
      echo
      echo "the database is empty until you run: nix run .#ingest"
    '';
  };

  down = pkgs.writeShellApplication {
    name = "dev-down";
    runtimeInputs = [
      pkgs.podman
      pkgs.podman-compose
    ];
    text = ''
      # --volumes is the point of this command: the requirement is that the
      # environment can be destroyed without leaving anything behind.
      ${podmanCompose} down --volumes --remove-orphans
      podman image rm --force "sourceslist-web:${version}" "sourceslist-ingest:${version}" 2>/dev/null || true
      echo "removed containers, volumes, network, and images"
    '';
  };

  ingest = pkgs.writeShellApplication {
    name = "dev-ingest";
    runtimeInputs = [
      pkgs.podman
      pkgs.podman-compose
    ];
    text = ''
      echo "this downloads the UDD dump, 2.4 GiB, from udd.debian.org"
      ${podmanCompose} --profile manual run --rm ingest "$@"
    '';
  };
}
