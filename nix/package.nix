{
  lib,
  buildGoModule,
  src,
  version ? "0.1.0",
}:

# The single Go build for the repository. Both binaries come from it, and the
# checks reuse its vendored dependencies so that `nix flake check` runs without
# network access.
buildGoModule {
  pname = "sourceslist";
  inherit version src;

  # Update with the hash Nix prints when go.mod changes. It pins the dependency
  # closure independently of the module proxy, so a build cannot be changed by
  # anything happening upstream after the fact.
  vendorHash = "sha256-904l7LfgjynBGYgWNeM2ltHuquifgNQWfWzoXiXAoHM=";

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  meta = {
    description = "Browse Debian packages, bug reports, and installation images";
    homepage = "https://github.com/fnune/sourcesli.st";
    mainProgram = "ingest";
    platforms = lib.platforms.unix;
  };
}
