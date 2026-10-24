{
  description = "Google style monorepo";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    nixpkgs,
    flake-utils,
    ...
  }:
    flake-utils.lib.eachDefaultSystem (
      system: let
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };
        MedievalTools = with pkgs; [
          bazel_7
          arion
          docker-compose
          sops
          go
          templ
          tailwindcss
          air
          gnumake
        ];
      in {
        devShells.default = pkgs.mkShell {
          buildInputs = MedievalTools;

          shellHook = ''
            echo "⚡ Monorepo Environment Activated ⚡"
            echo "Available tools: $(bazel --version) | arion $(arion version)"
          '';
        };
      }
    );
}
