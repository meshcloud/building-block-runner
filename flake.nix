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

        shell_hook = ''
          export GOROOT="${pkgsUnstable.go_1_27}/share/go"
          # With auto, a newer go line in go.mod would download and run another Go than this one.
          export GOTOOLCHAIN=local
        '' + (if pkgs.stdenv.isDarwin then ''
          # getting rid of "warning: unhandled Platform key FamilyDisplayName" on macOS
          unset DEVELOPER_DIR
        '' else "");

        core_packages = [
          # Keep in step with the go line in go.mod and the golang images of the Dockerfiles.
          pkgsUnstable.go_1_27
          # No golangci-lint here: the Taskfile builds it with the Go that go.mod asks for.
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
