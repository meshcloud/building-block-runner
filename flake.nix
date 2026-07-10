{
  description = "A Nix flake for the building-block-runner developer shell";

  inputs = {
    nixpkgs.url = "nixpkgs/nixos-25.11";
    nixpkgs-unstable.url = "nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, nixpkgs-unstable, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        pkgsUnstable = import nixpkgs-unstable { inherit system; };

        shell_hook =
          if pkgs.stdenv.isDarwin then ''
            # getting rid of "warning: unhandled Platform key FamilyDisplayName" on macOS
            unset DEVELOPER_DIR
          '' else "";

        core_packages = [
          pkgs.go
          pkgs.golangci-lint
          pkgs.go-task # task runner (replaces the Makefile, see Taskfile.yml / D14)
          pkgs.jdk21_headless
          pkgs.opentofu
          pkgs.minikube

          # kotlin linter
          # Keep this version in sync with the ktlint { version } block in build.gradle
          # and the ktlint --format hook in .claude/settings.json.
          # nixos-25.11 stable only ships 1.7.1; unstable currently provides 1.8.0.
          pkgsUnstable.ktlint
        ];
      in
      {
        devShells.default = pkgs.mkShell {
          name = "building-block-runner shell";
          packages = core_packages;
          hardeningDisable = [ "fortify" ]; # to be able to debug golang, c.f. https://nixos.wiki/wiki/Go
          shellHook = shell_hook;
        };
      }
    );
}
