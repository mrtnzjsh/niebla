.PHONY: all dev build test clean

all: build

dev:
	@$(MAKE) -C citc-toolchain dev

build:
	@$(MAKE) -C citc-toolchain build

test:
	@$(MAKE) -C citc-toolchain test

clean:
	@$(MAKE) -C citc-toolchain clean
