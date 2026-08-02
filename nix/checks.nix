{
  pkgs,
  package,
}:

{
  # buildGoModule runs `go test ./...` in its check phase against the vendored
  # dependency closure, so building the package is the test run. No second
  # derivation and no network.
  tests = package;

  # Linting reuses the same vendored modules. Without that it would try to reach
  # the module proxy, which the sandbox forbids, and a check that cannot run is
  # worse than no check.
  lint = package.overrideAttrs (previous: {
    pname = "check-golangci-lint";
    nativeBuildInputs = (previous.nativeBuildInputs or [ ]) ++ [ pkgs.golangci-lint ];

    buildPhase = ''
      runHook preBuild
      export HOME="$TMPDIR"
      export GOLANGCI_LINT_CACHE="$TMPDIR/golangci-cache"
      export GOFLAGS="-mod=vendor"
      golangci-lint run ./...
      runHook postBuild
    '';

    checkPhase = "true";

    installPhase = ''
      runHook preInstall
      touch $out
      runHook postInstall
    '';
  });

  # govulncheck is deliberately absent. It reads the Go vulnerability database
  # over the network, which the Nix sandbox forbids, and a check that cannot
  # reach its data is a check that always passes. It runs as its own CI step
  # instead, where a failure means something.
}
