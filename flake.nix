{
  description = "A Nix flake for the building-block-runner developer shell";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        shell_hook = ''
          export GOROOT="${pkgs.go_1_27}/share/go"
          # With auto, a newer go line in go.mod would download and run another Go than this one.
          export GOTOOLCHAIN=local
        '' + (if pkgs.stdenv.isDarwin then ''
          # getting rid of "warning: unhandled Platform key FamilyDisplayName" on macOS
          unset DEVELOPER_DIR
        '' else "");

        core_packages = [
          # CI builds with this Go. Keep it in step with the go line in go.mod and the golang image of the Dockerfile.
          pkgs.go_1_27
          # No golangci-lint here: it is a tool directive in go.mod, built by `task lint`.
          pkgs.go-task
          pkgs.opentofu
          pkgs.minikube
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
