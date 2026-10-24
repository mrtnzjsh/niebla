{
  description = "CitC unified toolchain (server, fuse, cli)";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
    nixpkgs,
    flake-utils,
  }:
    flake-utils.lib.eachDefaultSystem (system: let
      pkgs = nixpkgs.legacyPackages.${system};

      fusePkg = pkgs.buildGoModule {
        pname = "citcfs";
        version = "0.1.0";
        src = ./.;
        subPackages = [ "cmd/citcfs" ];
        vendorHash = null;
      };

      serverPkg = pkgs.buildGoModule {
        pname = "citc-server";
        version = "0.1.0";
        src = ./.;
        subPackages = [ "cmd/citc-server" ];
        vendorHash = null;
      };

      goEnv = pkgs.mkShell {
        buildInputs = with pkgs; [
          go
          protobuf
          protoc-gen-go
          protoc-gen-go-grpc
          fuse3
        ];

        shellHook = ''
          echo "CitC Toolchain Go Development Environment"

          # Set up Go environment
          export GOPATH=$HOME/go
          export PATH=$GOPATH/bin:$PATH

          echo "Go version: $(go version)"
          echo ""
          echo "Available commands:"
          echo "  go build ./cmd/citc-server - Build the CitC gRPC repository server"
          echo "  go build ./cmd/citcfs     - Build the citcfs FUSE daemon"
          echo "  go test ./...             - Run tests"
          echo "  go run ./cmd/citcfs       - Run the citcfs FUSE daemon"
        '';
      };
    in {
      packages = {
        server = serverPkg;
        fuse = fusePkg;
        default = serverPkg;
      };

      devShells.default = goEnv;
    });
}
