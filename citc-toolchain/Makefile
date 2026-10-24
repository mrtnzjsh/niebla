SHELL := /bin/sh
.PHONY: dev build test clean generate css dev-templ dev-css dev-server

all: build

generate:
	templ generate

css:
	@mkdir -p assets/static
	tailwindcss -i ./assets/css/input.css -o ./assets/static/styles.css --minify

build: generate css
	@mkdir -p bin
	go build -o bin/critique ./cmd/critique
	go build -o bin/citc-server ./cmd/citc-server
	go build -o bin/citc ./cmd/citc

test: generate
	go test -v ./...

clean:
	rm -rf tmp/ bin/ assets/static/styles.css internal/web/views/*_templ.go

dev-templ:
	templ generate --watch --proxy="http://127.0.0.1:8080" --proxyport=7331 --proxybind="0.0.0.0" --open-browser=false

dev-css:
	tailwindcss -i ./assets/css/input.css -o ./assets/static/styles.css --watch

dev-server:
	air

dev:
	@echo "Starting Critique hot-reloading development environment on proxy port 7331..."
	@mkdir -p tmp assets/static
	@which templ >/dev/null 2>&1 || (echo "Error: 'templ' command not found. Enter 'nix develop' first." && exit 1)
	@which tailwindcss >/dev/null 2>&1 || (echo "Error: 'tailwindcss' command not found. Enter 'nix develop' first." && exit 1)
	@which air >/dev/null 2>&1 || (echo "Error: 'air' command not found. Enter 'nix develop' first." && exit 1)
	@trap 'kill 0' EXIT INT TERM; \
	templ generate --watch --proxy="http://127.0.0.1:8080" --proxyport=7331 --proxybind="0.0.0.0" --open-browser=false & \
	tailwindcss -i ./assets/css/input.css -o ./assets/static/styles.css --watch & \
	air & \
	wait
