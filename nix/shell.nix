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
  ];

  env = {
    CGO_ENABLED = "0";
  };

  shellHook = ''
    echo "sourcesli.st dev shell"
    echo "  nix run .#dev-up     start the environment"
    echo "  nix run .#dev-down   destroy it"
    echo "  nix fmt              format everything"
    echo "  nix flake check      run the full baseline"
  '';
}
