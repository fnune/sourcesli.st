{ pkgs, ... }:

{
  projectRootFile = "flake.nix";

  programs = {
    gofumpt.enable = true;
    nixfmt.enable = true;
    shfmt.enable = true;
    yamlfmt.enable = true;
    taplo.enable = true;
    sqlfluff = {
      enable = true;
      dialect = "postgres";
    };
  };

  settings.formatter = {
    shfmt.options = [
      "--indent"
      "2"
    ];
  };

  settings.global.excludes = [
    "*.lock"
    "flake.lock"
    "go.sum"
    "LICENSE"
    "web/assets/vendor/*"
    "testdata/**"
    # Generated files are byte-exact tool output. CI regenerates them and
    # fails on any diff, so a formatter touching them turns every honest
    # regeneration into drift.
    "internal/store/query/*"
    "web/assets/app.css"
  ];
}
