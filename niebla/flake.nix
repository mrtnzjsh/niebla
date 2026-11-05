{
  description = "Niebla unified toolchain (server, fuse, cli)";

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
        pname = "nieblafs";
        version = "0.1.0";
        src = ./.;
        subPackages = [ "cmd/nieblafs" ];
        vendorHash = null;
      };

      serverPkg = pkgs.buildGoModule {
        pname = "niebla-server";
        version = "0.1.0";
        src = ./.;
        subPackages = [ "cmd/niebla-server" ];
        vendorHash = null;
        nativeBuildInputs = [ pkgs.makeWrapper ];
        postInstall = ''
          wrapProgram $out/bin/niebla-server \
            --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.postgresql_16 ]}
        '';
      };

      cliPkg = pkgs.buildGoModule {
        pname = "niebla";
        version = "0.1.0";
        src = ./.;
        subPackages = [ "cmd/niebla" ];
        vendorHash = null;
        postInstall = ''
          mkdir -p $out/share/niebla/skills/niebla
          if [ -d skills/niebla ]; then
            cp -r skills/niebla/* $out/share/niebla/skills/niebla/
          fi
        '';
      };

      cotejoPkg = pkgs.buildGoModule {
        pname = "cotejo";
        version = "0.1.0";
        src = ./.;
        subPackages = [ "cmd/cotejo" ];
        vendorHash = null;
      };

      goEnv = pkgs.mkShell {
        buildInputs = with pkgs; [
          go
          sqlc
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
          echo "Niebla Unified Toolchain Go Development Environment"

          # Set up Go environment
          export GOPATH=$HOME/go
          export PATH=$GOPATH/bin:$PATH

          echo "Go version: $(go version)"
          echo ""
          echo "Available commands:"
          echo "  go build ./cmd/niebla-server - Build the Niebla gRPC repository server"
          echo "  go build ./cmd/nieblafs      - Build the NieblaFS FUSE daemon"
          echo "  go build ./cmd/cotejo        - Build the Cotejo code review web server"
          echo "  go build ./cmd/niebla        - Build the Niebla CLI"
          echo "  go test ./...                - Run tests"
          echo "  make dev-cotejo              - Run Cotejo web server with live reload"
          echo "  make build                   - Build all 4 binaries into ./bin"
        '';
      };
    in {
      packages = {
        server = serverPkg;
        fuse = fusePkg;
        cli = cliPkg;
        niebla = cliPkg;
        nieblafs = fusePkg;
        "niebla-server" = serverPkg;
        cotejo = cotejoPkg;
        default = cliPkg;
      };

      devShells.default = goEnv;
    }) // {
      homeManagerModules.default = { config, lib, pkgs, ... }: {
        options.programs.niebla = {
          enable = lib.mkEnableOption "Niebla developer CLI";
          package = lib.mkOption {
            type = lib.types.package;
            default = self.packages.${pkgs.system}.cli;
            description = "The niebla package to install";
          };
          enableSkills = lib.mkOption {
            type = lib.types.bool;
            default = true;
            description = "Whether to link agent skills for Gemini/Antigravity, Claude Code, and XDG agents";
          };
        };

        config = lib.mkIf config.programs.niebla.enable {
          home.packages = [ config.programs.niebla.package ];

          xdg.configFile."gemini/config/skills/niebla".source = lib.mkIf config.programs.niebla.enableSkills
            "${config.programs.niebla.package}/share/niebla/skills/niebla";
          xdg.configFile."agents/skills/niebla".source = lib.mkIf config.programs.niebla.enableSkills
            "${config.programs.niebla.package}/share/niebla/skills/niebla";
        };
      };
    };
}
