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

      cliPkg = pkgs.buildGoModule {
        pname = "citc";
        version = "0.1.0";
        src = ./.;
        subPackages = [ "cmd/citc" ];
        vendorHash = null;
        postInstall = ''
          mkdir -p $out/share/citc/skills/citc
          cp -r skills/citc/* $out/share/citc/skills/citc/
        '';
      };

      critiquePkg = pkgs.buildGoModule {
        pname = "critique";
        version = "0.1.0";
        src = ./.;
        subPackages = [ "cmd/critique" ];
        vendorHash = null;
      };

      goEnv = pkgs.mkShell {
        buildInputs = with pkgs; [
          go
          protobuf
          protoc-gen-go
          protoc-gen-go-grpc
          fuse3
          templ
          tailwindcss
          air
          chromium
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
          echo "  go build ./cmd/critique   - Build the Critique code review web server"
          echo "  go test ./...             - Run tests"
          echo "  go run ./cmd/citcfs       - Run the citcfs FUSE daemon"
          echo "  make dev                  - Run Critique web server with live reload"
        '';
      };
    in {
      packages = {
        server = serverPkg;
        fuse = fusePkg;
        cli = cliPkg;
        citc = cliPkg;
        critique = critiquePkg;
        default = cliPkg;
      };

      devShells.default = goEnv;
    }) // {
      homeManagerModules.default = { config, lib, pkgs, ... }: {
        options.programs.citc = {
          enable = lib.mkEnableOption "CitC developer CLI";
          package = lib.mkOption {
            type = lib.types.package;
            default = self.packages.${pkgs.system}.cli;
            description = "The citc package to install";
          };
          enableSkills = lib.mkOption {
            type = lib.types.bool;
            default = true;
            description = "Whether to link agent skills for Gemini/Antigravity, Claude Code, and XDG agents";
          };
        };

        config = lib.mkIf config.programs.citc.enable {
          home.packages = [ config.programs.citc.package ];

          xdg.configFile."gemini/config/skills/citc".source = lib.mkIf config.programs.citc.enableSkills
            "${config.programs.citc.package}/share/citc/skills/citc";
          xdg.configFile."agents/skills/citc".source = lib.mkIf config.programs.citc.enableSkills
            "${config.programs.citc.package}/share/citc/skills/citc";
        };
      };
    };
}
