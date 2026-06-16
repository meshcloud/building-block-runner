{
  description = "A Nix flake for the building-block-runner developer shell";

  inputs = {
    nixpkgs.url = "nixpkgs/nixos-25.11";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        shell_hook =
          if pkgs.stdenv.isDarwin then ''
            # getting rid of "warning: unhandled Platform key FamilyDisplayName" on macOS
            unset DEVELOPER_DIR
          '' else "";

        core_packages = [
          pkgs.go
          pkgs.golangci-lint
          pkgs.jdk21_headless
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
