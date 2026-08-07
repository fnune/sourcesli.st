{
  mkShell,
  go,
  gopls,
  gotools,
  gofumpt,
  golangci-lint,
  go-tools,
  govulncheck,
  sqlc,
  postgresql,
  podman,
  podman-compose,
  tailwindcss_4,
  pulumi,
  curl,
  jq,
  just,
}:

mkShell {
  packages = [
    go
    gopls
    gotools
    gofumpt
    golangci-lint
    go-tools
    govulncheck
    sqlc
    postgresql
    podman
    podman-compose
    tailwindcss_4
    pulumi
    curl
    jq
    just
  ];

  env = {
    CGO_ENABLED = "0";
  };

  shellHook = ''
    echo "sourcesli.st dev shell"
    echo "  just dev             database, schema, and the site on :8080"
    echo "  just restore         fill it from the newest local snapshot"
    echo "  just --list          every command in this repository"
    echo "  just check           run the full baseline"
  '';
}
