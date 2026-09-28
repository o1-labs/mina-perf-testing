# Local perf-testing environment. See README.md.

MINA_IMAGE ?= minaprotocol/mina-daemon:4.0.0-6965b50-jammy-devnet
LOCAL_KEY_PASS ?= local
# full: seed, bp-1, bp-2, plain-1, plain-2. minimal: seed (also the block
# producer) and plain-1, for small machines and CI.
TOPOLOGY ?= full
export MINA_IMAGE LOCAL_KEY_PASS

ifeq ($(TOPOLOGY),full)
PROFILES := full
else ifeq ($(TOPOLOGY),minimal)
PROFILES :=
else
$(error TOPOLOGY must be full or minimal, not '$(TOPOLOGY)')
endif

COMPOSE := COMPOSE_PROFILES=$(PROFILES) docker compose -p perf-local
# Every profile, so that stop and removal also reach the containers of a
# topology that is not the current one.
COMPOSE_ALL := COMPOSE_PROFILES=full docker compose -p perf-local
ALL_NODES := seed bp-1 bp-2 plain-1 plain-2

# Runs local-env.sh in the Mina image as the calling user, so generated files
# stay owned by them.
LOCAL_ENV := docker run --rm -u $(shell id -u):$(shell id -g) \
	-e MINA_PRIVKEY_PASS=$(LOCAL_KEY_PASS) -e OUT=/out -e TOPOLOGY=$(TOPOLOGY) \
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
	$(COMPOSE_ALL) pull --ignore-buildable --quiet
	$(COMPOSE_ALL) build

# Every `up` starts a new chain: the genesis timestamp is fresh, and the
# nodes are recreated without state. All nodes are removed first, so a node
# of the other topology cannot stay on the old chain. Postgres keeps its
# data; `make clean` drops it.
up: images
	$(COMPOSE_ALL) rm -sf $(ALL_NODES)
	$(LOCAL_ENV) genesis
	$(COMPOSE) up -d --force-recreate
	@echo "genesis: $$(grep GENESIS_TIMESTAMP generated/nodes.env | cut -d= -f2), topology: $(TOPOLOGY)"
	@echo "orchestrator http://localhost:9090  dashboard http://localhost:4200"

down:
	$(COMPOSE_ALL) down

status:
	@$(COMPOSE_ALL) ps --format 'table {{.Service}}\t{{.Status}}'
	@$(COMPOSE) run --rm -T tools scripts/smoke.sh status

smoke:
	$(COMPOSE) run --rm -T tools scripts/smoke.sh run

logs:
	$(COMPOSE_ALL) logs -f --tail 50

clean:
	$(COMPOSE_ALL) down -v
	rm -rf generated
