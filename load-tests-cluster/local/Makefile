# Local perf-testing environment. See README.md.

MINA_IMAGE ?= minaprotocol/mina-daemon:4.0.0-6965b50-jammy-devnet
LOCAL_KEY_PASS ?= local
export MINA_IMAGE LOCAL_KEY_PASS

COMPOSE := docker compose -p perf-local
NODES := seed bp-1 bp-2 plain-1 plain-2

# Runs local-env.sh in the Mina image as the calling user, so generated files
# stay owned by them.
LOCAL_ENV := docker run --rm -u $(shell id -u):$(shell id -g) \
	-e MINA_PRIVKEY_PASS=$(LOCAL_KEY_PASS) -e OUT=/out \
	-v $(CURDIR)/generated:/out -v $(CURDIR)/scripts:/scripts:ro \
	--entrypoint bash $(MINA_IMAGE) /scripts/local-env.sh

.PHONY: up down images keys genesis status smoke logs clean

generated:
	mkdir -p generated

keys: | generated
	$(LOCAL_ENV) keys

genesis: keys
	$(LOCAL_ENV) genesis

# Pulls and builds first, then writes the genesis, so that the genesis delay
# starts when the containers do. Compose needs generated/nodes.env even to
# build, hence the first `genesis` when it is missing.
images: keys
	@[ -f generated/nodes.env ] || $(LOCAL_ENV) genesis
	$(COMPOSE) pull --ignore-buildable --quiet
	$(COMPOSE) build

# Every `up` starts a new chain: the genesis timestamp is fresh, and the
# nodes are recreated without state. Postgres keeps its data; `make clean`
# drops it.
up: images
	$(LOCAL_ENV) genesis
	$(COMPOSE) up -d --force-recreate
	@echo "genesis: $$(grep GENESIS_TIMESTAMP generated/nodes.env | cut -d= -f2)"
	@echo "orchestrator http://localhost:9090  dashboard http://localhost:4200"

down:
	$(COMPOSE) down

status:
	@$(COMPOSE) ps --format 'table {{.Service}}\t{{.Status}}'
	@$(COMPOSE) run --rm -T tools scripts/smoke.sh status

smoke:
	$(COMPOSE) run --rm -T tools scripts/smoke.sh run

logs:
	$(COMPOSE) logs -f --tail 50

clean:
	$(COMPOSE) down -v
	rm -rf generated
