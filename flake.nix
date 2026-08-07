{
  description = "sourcesli.st: browse Debian packages, bug reports, and installation images";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      treefmt-nix,
    }:
    let
      version = "0.1.0";

      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];

      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});

      # Images and the container development environment are Linux only. On
      # macOS the dev shell and the checks still work, which is what a
      # contributor on that platform actually needs.
      forLinux =
        f:
        nixpkgs.lib.genAttrs (builtins.filter (nixpkgs.lib.hasSuffix "linux") systems) (
          system: f nixpkgs.legacyPackages.${system}
        );

      treefmtFor = pkgs: treefmt-nix.lib.evalModule pkgs ./nix/treefmt.nix;

      packageFor =
        pkgs:
        pkgs.callPackage ./nix/package.nix {
          inherit version;
          src = self;
        };

      imagesFor =
        pkgs:
        import ./nix/images.nix {
          inherit pkgs version;
          package = packageFor pkgs;
        };

      devFor =
        pkgs:
        import ./nix/dev.nix {
          inherit pkgs version;
          images = imagesFor pkgs;
        };
    in
    {
      formatter = forAllSystems (pkgs: (treefmtFor pkgs).config.build.wrapper);

      devShells = forAllSystems (pkgs: {
        default = pkgs.callPackage ./nix/shell.nix { };
      });

      packages = forAllSystems (
        pkgs:
        {
          default = packageFor pkgs;
        }
        // nixpkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          images-web = (imagesFor pkgs).web;
          images-ingest = (imagesFor pkgs).ingest;
        }
      );

      apps = forLinux (pkgs: {
        dev-up = {
          type = "app";
          program = nixpkgs.lib.getExe (devFor pkgs).up;
          meta.description = "Start the development database";
        };
        dev-stack = {
          type = "app";
          program = nixpkgs.lib.getExe (devFor pkgs).stack;
          meta.description = "Build the images and run the whole stack in containers";
        };
        dev-stop = {
          type = "app";
          program = nixpkgs.lib.getExe (devFor pkgs).stop;
          meta.description = "Stop the containers, keeping the database";
        };
        dev-down = {
          type = "app";
          program = nixpkgs.lib.getExe (devFor pkgs).down;
          meta.description = "Destroy the development environment, volumes and images included";
        };
        ingest = {
          type = "app";
          program = nixpkgs.lib.getExe (devFor pkgs).ingest;
          meta.description = "Run one ETL pass against the published UDD dump";
        };
      });

      checks = forAllSystems (
        pkgs:
        {
          formatting = (treefmtFor pkgs).config.build.check self;
        }
        // import ./nix/checks.nix {
          inherit pkgs;
          package = packageFor pkgs;
        }
      );
    };
}
