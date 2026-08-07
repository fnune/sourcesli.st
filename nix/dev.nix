{
  pkgs,
  images,
  version,
}:

let
  postgresImage = "docker.io/library/postgres:18-alpine";

  # Uncommon, so the environment never loses a race against a system Postgres.
  databasePort = "15432";
  stackWebPort = "18080";

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
        volumes = [ "pgdata:/var/lib/postgresql" ];
        # Published on loopback only. The development environment must not be
        # reachable from anything but this machine.
        ports = [ "127.0.0.1:${databasePort}:5432" ];
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
        profiles = [ "stack" ];
        depends_on.database.condition = "service_healthy";
        environment = {
          DATABASE_URL = "postgres://sourceslist:devonly@database:5432/sourceslist";
          LISTEN_ADDRESS = "0.0.0.0:8080";
        };
        ports = [ "127.0.0.1:${stackWebPort}:8080" ];
      };

      # A job rather than a service: it runs once and exits. The `manual`
      # profile keeps `dev-up` from starting it, because a 2.4 GiB download is
      # not something that should happen because somebody booted the stack.
      #
      # Its cache is bind-mounted by the wrapper rather than declared here, so a
      # teardown cannot take the download with it.
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
      };
    };

    volumes.pgdata = { };
  };

  composeFile = pkgs.writeText "docker-compose.json" compose;

  podmanCompose = "${pkgs.podman}/bin/podman compose --file ${composeFile}";

  loadImages = ''
    ${pkgs.gzip}/bin/gzip -dc ${images.web} | podman load
    ${pkgs.gzip}/bin/gzip -dc ${images.ingest} | podman load
  '';
in
{
  up = pkgs.writeShellApplication {
    name = "dev-up";
    runtimeInputs = [
      pkgs.podman
      pkgs.podman-compose
    ];
    text = ''
      ${podmanCompose} up --detach database

      echo
      echo "database: postgres://sourceslist:devonly@127.0.0.1:${databasePort}/sourceslist"
    '';
  };

  stack = pkgs.writeShellApplication {
    name = "dev-stack";
    runtimeInputs = [
      pkgs.podman
      pkgs.podman-compose
      pkgs.gzip
    ];
    text = ''
      echo "loading images built by nix"
      ${loadImages}

      ${podmanCompose} --profile stack up --detach

      echo
      echo "stack: http://127.0.0.1:${stackWebPort}"
    '';
  };

  stop = pkgs.writeShellApplication {
    name = "dev-stop";
    runtimeInputs = [
      pkgs.podman
      pkgs.podman-compose
    ];
    text = ''
      ${podmanCompose} --profile stack stop
      echo "stopped. the database volume is untouched; dev-up brings it back"
    '';
  };

  down = pkgs.writeShellApplication {
    name = "dev-down";
    runtimeInputs = [
      pkgs.podman
      pkgs.podman-compose
    ];
    text = ''
      if [ "''${1:-}" != "--yes" ]; then
        echo "this destroys the database volume and everything ingested into it."
        echo "snapshot it first with 'just snapshot', or pass --yes to skip this."
        printf "destroy it? [y/N] "
        read -r reply
        case "$reply" in
          [yY]*) ;;
          *)
            echo "left alone"
            exit 0
            ;;
        esac
      fi

      # --volumes is the point of this command: the requirement is that the
      # environment can be destroyed without leaving anything behind.
      ${podmanCompose} --profile stack --profile manual down --volumes --remove-orphans
      podman image rm --force "sourceslist-web:${version}" "sourceslist-ingest:${version}" 2>/dev/null || true
      echo "removed containers, volumes, network, and images"
      echo "kept .dev/, which holds the dump cache and your snapshots"
    '';
  };

  ingest = pkgs.writeShellApplication {
    name = "dev-ingest";
    runtimeInputs = [
      pkgs.podman
      pkgs.podman-compose
      pkgs.gzip
    ];
    text = ''
      echo "loading images built by nix"
      ${loadImages}

      cache="$PWD/.dev/cache"
      mkdir -p "$cache"

      echo "the dump is 2.4 GiB. it is cached in .dev/cache, so a second run"
      echo "transfers no body and rebuilds nothing."
      ${podmanCompose} --profile manual run --rm \
        --volume "$cache:/var/cache/sourceslist" \
        ingest "$@"
    '';
  };
}
