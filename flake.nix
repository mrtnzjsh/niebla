{
  description = "Niebla unified version control and code review toolchain";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = {
    self,
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

        vendorHash = "sha256-DdBpdSz9WHzmztQvMunVr1dmOkGh3FphKOeUHnLzrwQ=";

        fusePkg = pkgs.buildGoModule {
          pname = "nieblafs";
          version = "0.1.0";
          src = ./niebla;
          subPackages = [ "cmd/nieblafs" ];
          inherit vendorHash;
        };

        serverPkg = pkgs.buildGoModule {
          pname = "niebla-server";
          version = "0.1.0";
          src = ./niebla;
          subPackages = [ "cmd/niebla-server" ];
          inherit vendorHash;
          nativeBuildInputs = [ pkgs.makeWrapper ];
          postInstall = ''
            wrapProgram $out/bin/niebla-server \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.postgresql_16 ]}
          '';
        };

        cotejoPkg = pkgs.buildGoModule {
          pname = "cotejo";
          version = "0.1.0";
          src = ./niebla;
          subPackages = [ "cmd/cotejo" ];
          inherit vendorHash;
        };

        cliPkg = pkgs.buildGoModule {
          pname = "niebla";
          version = "0.1.0";
          src = ./niebla;
          subPackages = [ "cmd/niebla" ];
          inherit vendorHash;
          postInstall = ''
            mkdir -p $out/share/niebla/skills
            if [ -d skills ]; then
              cp -rL skills/* $out/share/niebla/skills/
            fi
          '';
        };

        allPkg = pkgs.symlinkJoin {
          name = "niebla-suite-0.1.0";
          paths = [ cliPkg serverPkg fusePkg cotejoPkg ];
        };

        setupScript = pkgs.writeShellScriptBin "niebla-setup" ''
          set -euo pipefail

          echo "=========================================================="
          echo "   Niebla Clean Install & Environment Setup"
          echo "=========================================================="

          # 1. Resolve PostgreSQL Port
          DETECTED_PORT=5432
          if (echo > /dev/tcp/127.0.0.1/5432) 2>/dev/null; then
            echo "[i] Port 5432 is already in use by another service on this system."
            DETECTED_PORT=5433
          else
            echo "[✓] Port 5432 is free."
          fi

          DB_PORT="''${NIEBLA_DB_PORT:-$DETECTED_PORT}"
          echo "[✓] Selected PostgreSQL port for Niebla: $DB_PORT"

          # 2. Configure ~/.config/niebla/config.toml
          CONFIG_DIR="''${XDG_CONFIG_HOME:-$HOME/.config}/niebla"
          CONFIG_FILE="$CONFIG_DIR/config.toml"
          mkdir -p "$CONFIG_DIR"

          if [ ! -f "$CONFIG_FILE" ]; then
            echo "[+] Initializing clean $CONFIG_FILE..."
            cat <<EOF > "$CONFIG_FILE"
version = 1

[core]
server_addr = "127.0.0.1:50051"
default_workspace = "default"

[server]
host = "0.0.0.0"
port = 50051
db_url = "postgres://niebla:niebla@127.0.0.1:$DB_PORT/niebla?sslmode=disable"
blob_path = "$HOME/.cache/niebla/blobs"
cache_size = 10000

[fs]
mount_base = "$HOME/niebla/workspaces"
cache_dir = "$HOME/.cache/niebla/blobs"

[critique]
host = "0.0.0.0"
port = 8080
proxy_port = 7331
default_reviewer = "\$USER"
required_approvals = 0
allow_submit_with_unresolved_comments = false

[critique.auth]
mode = "local"
default_user = "\$USER"

[critique.theme]
preset = "catppuccin-mocha"
syntax_style = "catppuccin-mocha"
EOF
          else
            echo "[✓] Updating database port in existing $CONFIG_FILE to $DB_PORT..."
            sed -i -E "s|127\.0\.0\.1:[0-9]+/|127.0.0.1:$DB_PORT/|g" "$CONFIG_FILE"
          fi

          # 3. Clean up legacy prototype artifacts
          echo "[*] Ensuring stale prototype artifacts are removed..."
          rm -rf "$HOME/.gemini/config/skills/citc" "$HOME/.gemini/config/skills/citc-config" "$HOME/.local/share/citc" 2>/dev/null || true

          # 4. Copy and synchronize the 4 official Niebla skills
          echo "[*] Synchronizing Niebla agent skills..."
          SKILLS_SRC="${cliPkg}/share/niebla/skills"

          GEMINI_SKILLS="$HOME/.gemini/config/skills"
          CLAUDE_SKILLS="$HOME/.claude/skills"
          AGENTS_SKILLS="''${XDG_CONFIG_HOME:-$HOME/.config}/agents/skills"
          CURSOR_RULES="$HOME/.cursor/rules"

          mkdir -p "$GEMINI_SKILLS" "$CLAUDE_SKILLS" "$AGENTS_SKILLS" "$CURSOR_RULES"

          for skill in niebla niebla-ci niebla-config niebla-review-agent; do
            if [ -d "$SKILLS_SRC/$skill" ]; then
              # Gemini / Antigravity
              rm -rf "$GEMINI_SKILLS/$skill"
              cp -rL "$SKILLS_SRC/$skill" "$GEMINI_SKILLS/$skill"

              # Claude Code
              rm -rf "$CLAUDE_SKILLS/$skill"
              cp -rL "$SKILLS_SRC/$skill" "$CLAUDE_SKILLS/$skill"

              # Universal .agents
              rm -rf "$AGENTS_SKILLS/$skill"
              cp -rL "$SKILLS_SRC/$skill" "$AGENTS_SKILLS/$skill"

              # Cursor Rules (.mdc)
              if [ -f "$SKILLS_SRC/$skill/SKILL.md" ]; then
                cp -L "$SKILLS_SRC/$skill/SKILL.md" "$CURSOR_RULES/$skill.mdc"
              fi
              echo "  [✓] Deployed skill: $skill"
            fi
          done

          # 5. Database Status & Next Steps
          echo ""
          echo "=========================================================="
          echo "   Setup Verification & Next Steps"
          echo "=========================================================="
          if (echo > /dev/tcp/127.0.0.1/$DB_PORT) 2>/dev/null; then
            echo "[✓] PostgreSQL is active and listening on port $DB_PORT."
          else
            echo "[!] PostgreSQL is not currently listening on port $DB_PORT."
            echo ""
            echo "To start PostgreSQL for Niebla on port $DB_PORT:"
            echo "  A) Via NixOS configuration.nix (Declarative & Isolated):"
            echo "       services.niebla = {"
            echo "         enable = true;"
            echo "         postgres.port = $DB_PORT;"
            echo "       };"
            echo "  B) Via Docker/Podman container:"
            echo "       docker run -d --name niebla-postgres -p $DB_PORT:5432 \\"
            echo "         -e POSTGRES_USER=niebla -e POSTGRES_PASSWORD=niebla -e POSTGRES_DB=niebla \\"
            echo "         postgres:16-alpine"
          fi
          echo ""
          echo "[✓] Configuration path: $CONFIG_FILE"
          echo "[✓] Skills installed for: Antigravity, Claude Code, Agents, Cursor"
          echo "=========================================================="
        '';

        devTools = with pkgs; [
          go
          sqlc
          protobuf
          protoc-gen-go
          protoc-gen-go-grpc
          fuse3
          postgresql_16
          templ
          tailwindcss
          air
          gnumake
        ];
      in {
        packages = {
          niebla = cliPkg;
          niebla-server = serverPkg;
          nieblafs = fusePkg;
          cotejo = cotejoPkg;
          all = allPkg;
          default = cliPkg;
        };

        apps = {
          setup = {
            type = "app";
            program = "${setupScript}/bin/niebla-setup";
          };
          default = self.apps.${system}.setup;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = devTools;

          shellHook = ''
            echo "⚡ Niebla Development Environment Activated ⚡"
            echo "Available tools: $(go version) | $(psql --version 2>/dev/null || echo 'PostgreSQL 16') | templ"
          '';
        };
      }
    ) // {
      nixosModules = {
        default = { config, lib, pkgs, ... }:
          let
            cfg = config.services.niebla;
            nieblaPkg = self.packages.${pkgs.system}.all;
            cliPkg = self.packages.${pkgs.system}.niebla;
          in {
            options.services.niebla = {
              enable = lib.mkEnableOption "Niebla unified version control and code review suite";

              package = lib.mkOption {
                type = lib.types.package;
                default = nieblaPkg;
                description = "Package providing niebla-server, nieblafs, cotejo, and niebla binaries.";
              };

              dataDir = lib.mkOption {
                type = lib.types.str;
                default = "/var/lib/niebla";
                description = "Root state directory for Niebla services and data.";
              };

              configFile = lib.mkOption {
                type = lib.types.path;
                default = (pkgs.formats.toml {}).generate "niebla-config.toml" {
                  version = 1;
                  core = {
                    server_addr = "${cfg.server.host}:${toString cfg.server.port}";
                    default_workspace = "default";
                  };
                  server = {
                    host = cfg.server.host;
                    port = cfg.server.port;
                    db_url = "postgres://${cfg.postgres.user}:${cfg.postgres.user}@127.0.0.1:${toString cfg.postgres.port}/${cfg.postgres.database}?sslmode=disable";
                    blob_path = "${cfg.dataDir}/blobs";
                    cache_size = 10000;
                  };
                  fs = {
                    mount_base = "/niebla";
                    cache_dir = "${cfg.dataDir}/blobs";
                  };
                  critique = {
                    host = cfg.cotejo.host;
                    port = cfg.cotejo.port;
                    proxy_port = 7331;
                    default_reviewer = "$USER";
                    required_approvals = 0;
                    allow_submit_with_unresolved_comments = false;
                  };
                  "critique.auth" = {
                    mode = "local";
                    default_user = "$USER";
                  };
                  "critique.theme" = {
                    preset = "catppuccin-mocha";
                    syntax_style = "catppuccin-mocha";
                  };
                };
                description = "Path to the Niebla configuration file. Automatically generated by default.";
              };

              server = {
                enable = lib.mkOption {
                  type = lib.types.bool;
                  default = true;
                  description = "Enable niebla-server gRPC repository daemon.";
                };
                host = lib.mkOption {
                  type = lib.types.str;
                  default = "0.0.0.0";
                  description = "Bind address for niebla-server.";
                };
                port = lib.mkOption {
                  type = lib.types.port;
                  default = 50051;
                  description = "gRPC listen port for niebla-server.";
                };
              };

              cotejo = {
                enable = lib.mkOption {
                  type = lib.types.bool;
                  default = true;
                  description = "Enable Cotejo code review web interface.";
                };
                host = lib.mkOption {
                  type = lib.types.str;
                  default = "0.0.0.0";
                  description = "Bind address for Cotejo web server.";
                };
                port = lib.mkOption {
                  type = lib.types.port;
                  default = 8080;
                  description = "HTTP listen port for Cotejo web server.";
                };
              };

              postgres = {
                enable = lib.mkOption {
                  type = lib.types.bool;
                  default = true;
                  description = "Manage an isolated, dedicated PostgreSQL instance for Niebla.";
                };
                port = lib.mkOption {
                  type = lib.types.port;
                  default = 5433;
                  description = "TCP port for Niebla PostgreSQL. Defaults to 5433 to avoid conflicts if another PostgreSQL already runs on 5432.";
                };
                database = lib.mkOption {
                  type = lib.types.str;
                  default = "niebla";
                  description = "Database name for Niebla.";
                };
                user = lib.mkOption {
                  type = lib.types.str;
                  default = "niebla";
                  description = "Database user for Niebla.";
                };
              };

              client = {
                enable = lib.mkOption {
                  type = lib.types.bool;
                  default = true;
                  description = "Install niebla CLI and nieblafs FUSE tools into systemPackages.";
                };
              };

              skills = {
                enable = lib.mkOption {
                  type = lib.types.bool;
                  default = true;
                  description = "Deploy Niebla agent skills into system-wide and user agent directories.";
                };
              };
            };

            config = lib.mkIf cfg.enable {
              users.users.niebla = {
                isSystemUser = true;
                group = "niebla";
                home = cfg.dataDir;
                createHome = true;
                description = "Niebla service daemon user";
              };
              users.groups.niebla = {};

              environment.systemPackages = lib.mkIf cfg.client.enable [
                cfg.package
              ];

              # Dedicated, isolated PostgreSQL instance running on cfg.postgres.port (never conflicts with system postgresql)
              systemd.services.niebla-postgresql = lib.mkIf cfg.postgres.enable {
                description = "Niebla Dedicated PostgreSQL Instance";
                wantedBy = [ "multi-user.target" ];
                after = [ "network.target" ];
                serviceConfig = {
                  User = "niebla";
                  Group = "niebla";
                  StateDirectory = "niebla/postgres";
                  WorkingDirectory = "${cfg.dataDir}/postgres";
                  ExecStartPre = pkgs.writeShellScript "niebla-pg-init" ''
                    set -eu
                    DATA_DIR="${cfg.dataDir}/postgres"
                    if [ ! -s "$DATA_DIR/PG_VERSION" ]; then
                      echo "Initializing Niebla PostgreSQL cluster..."
                      ${pkgs.postgresql_16}/bin/initdb -D "$DATA_DIR" --auth=trust --no-instructions -U ${cfg.postgres.user}
                      ${pkgs.postgresql_16}/bin/pg_ctl -D "$DATA_DIR" -o "-p ${toString cfg.postgres.port} -k /tmp" start -w
                      ${pkgs.postgresql_16}/bin/createdb -h 127.0.0.1 -p ${toString cfg.postgres.port} -U ${cfg.postgres.user} ${cfg.postgres.database} || true
                      ${pkgs.postgresql_16}/bin/pg_ctl -D "$DATA_DIR" stop -w
                    fi
                  '';
                  ExecStart = "${pkgs.postgresql_16}/bin/postgres -D ${cfg.dataDir}/postgres -p ${toString cfg.postgres.port} -k /tmp";
                  Restart = "always";
                  RestartSec = "5s";
                };
              };

              systemd.services.niebla-server = lib.mkIf cfg.server.enable {
                description = "Niebla Repository gRPC Server";
                wantedBy = [ "multi-user.target" ];
                after = [ "network.target" ] ++ (lib.optional cfg.postgres.enable "niebla-postgresql.service");
                requires = lib.optional cfg.postgres.enable "niebla-postgresql.service";
                serviceConfig = {
                  User = "niebla";
                  Group = "niebla";
                  StateDirectory = "niebla";
                  ExecStart = "${cfg.package}/bin/niebla-server --config ${cfg.configFile}";
                  Restart = "on-failure";
                  RestartSec = "3s";
                };
              };

              systemd.services.cotejo = lib.mkIf cfg.cotejo.enable {
                description = "Cotejo Code Review Service";
                wantedBy = [ "multi-user.target" ];
                after = [ "network.target" "niebla-server.service" ];
                requires = lib.optional cfg.server.enable "niebla-server.service";
                serviceConfig = {
                  User = "niebla";
                  Group = "niebla";
                  StateDirectory = "niebla";
                  ExecStart = "${cfg.package}/bin/cotejo --config ${cfg.configFile}";
                  Restart = "on-failure";
                  RestartSec = "3s";
                };
              };

              # Expose skills to system environment
              environment.etc = lib.mkIf cfg.skills.enable {
                "niebla/skills".source = "${cliPkg}/share/niebla/skills";
              };
            };
          };
        niebla = self.nixosModules.default;
      };

      homeManagerModules = {
        default = { config, lib, pkgs, ... }:
          let
            cfg = config.programs.niebla;
            cliPkg = self.packages.${pkgs.system}.niebla;
            skillsPkg = "${cliPkg}/share/niebla/skills";
          in {
            options.programs.niebla = {
              enable = lib.mkEnableOption "Niebla developer CLI";
              package = lib.mkOption {
                type = lib.types.package;
                default = cliPkg;
                description = "The niebla CLI package to install";
              };
              enableSkills = lib.mkOption {
                type = lib.types.bool;
                default = true;
                description = "Whether to automatically link all 4 agent skills (niebla, niebla-ci, niebla-config, niebla-review-agent)";
              };
              postgresPort = lib.mkOption {
                type = lib.types.port;
                default = 5433;
                description = "Default PostgreSQL port for local development configuration.";
              };
            };

            config = lib.mkIf cfg.enable {
              home.packages = [ cfg.package ];

              xdg.configFile = lib.mkIf cfg.enableSkills {
                "gemini/config/skills/niebla".source = "${skillsPkg}/niebla";
                "gemini/config/skills/niebla-ci".source = "${skillsPkg}/niebla-ci";
                "gemini/config/skills/niebla-config".source = "${skillsPkg}/niebla-config";
                "gemini/config/skills/niebla-review-agent".source = "${skillsPkg}/niebla-review-agent";

                "agents/skills/niebla".source = "${skillsPkg}/niebla";
                "agents/skills/niebla-ci".source = "${skillsPkg}/niebla-ci";
                "agents/skills/niebla-config".source = "${skillsPkg}/niebla-config";
                "agents/skills/niebla-review-agent".source = "${skillsPkg}/niebla-review-agent";
              };
            };
          };
        niebla = self.homeManagerModules.default;
      };
    };
}
