.PHONY: cli cli-install cli-deb cli-test getter-test scanner-test all-test stack-up stack-down

cli:
	$(MAKE) -C services/cli build

cli-install:
	$(MAKE) -C services/cli install

cli-deb:
	$(MAKE) -C services/cli deb

cli-test:
	$(MAKE) -C services/cli test

getter-test:
	$(MAKE) -C services/getter test

scanner-test:
	$(MAKE) -C services/scanner test

all-test: cli-test getter-test scanner-test

stack-up:
	docker compose up -d --build

stack-down:
	docker compose down
