.PHONY: all dev dev-cotejo build test clean docs-dev docs-build

all: build

dev:
	@$(MAKE) -C niebla dev

dev-cotejo:
	@$(MAKE) -C niebla dev-cotejo

build:
	@$(MAKE) -C niebla build

test:
	@$(MAKE) -C niebla test

clean:
	@$(MAKE) -C niebla clean

docs-dev:
	@npm run dev --prefix docs-site

docs-build:
	@npm run build --prefix docs-site
