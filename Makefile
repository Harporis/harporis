.PHONY: cli cli-install cli-deb cli-test getter-test scanner-test writer-test all-test stack-up stack-down

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

writer-test:
	$(MAKE) -C services/writer test

all-test: cli-test getter-test scanner-test writer-test

stack-up:
	env UID=$$(id -u) GID=$$(id -g) docker compose up -d --build --wait

stack-down:
	docker compose down

stack-status:
	docker compose ps
	@echo
	@docker compose exec -T scanner wget -qO- http://localhost:9101/metrics 2>/dev/null \
		| grep -E '^scanner_(chunks_consumed|findings_published|active_scans|build_info)' \
		| head -10 || echo "scanner metrics unavailable"
	@echo
	@docker compose exec -T writer wget -qO- http://localhost:9102/metrics 2>/dev/null \
		| grep -E '^writer_(findings_consumed|sink_writes|active_scans|build_info)' \
		| head -10 || echo "writer metrics unavailable"
