# Local perf-testing environment. See README.md.

MINA_IMAGE ?= minaprotocol/mina-daemon:4.0.0-6965b50-jammy-devnet
LOCAL_KEY_PASS ?= local
# Password of the `local` user for the orchestrator's POST endpoints (basic
# auth; the users file is generated/keys/write-auth.htpasswd).
LOCAL_WRITE_PASS ?= local
# full: seed, bp-1, bp-2 and PLAIN_NODES plain nodes (plain-1, plain-2, ...).
# minimal: seed (also the block producer) and plain-1, for small machines
# and CI.
TOPOLOGY ?= full
# Number of plain nodes in the full topology (at least 1). Each daemon needs
# up to about 2.7 GB of memory during an experiment.
PLAIN_NODES ?= 2
# 1: also run an archive node (fed by the seed), its database, and Rosetta.
ARCHIVE ?= 0
# 1: the daemons have the ITN harness operations (MinaProtocol/mina#19616), so
# the orchestrator funds keys through ITN createAccounts on the seed and
# journals its scheduling handles. Needs a MINA_IMAGE with that daemon.
ITN_HARNESS ?= 0
# Recorded by the orchestrator service in every run (orchestrator_build).
ORCHESTRATOR_BUILD ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo local)
export MINA_IMAGE LOCAL_KEY_PASS LOCAL_WRITE_PASS ORCHESTRATOR_BUILD

, := ,

ifeq ($(TOPOLOGY),full)
PROFILES := full
else ifeq ($(TOPOLOGY),minimal)
PROFILES :=
else
$(error TOPOLOGY must be full or minimal, not '$(TOPOLOGY)')
endif
ifeq ($(ARCHIVE),1)
PROFILES := $(if $(PROFILES),$(PROFILES)$(,))archive
else ifneq ($(ARCHIVE),0)
$(error ARCHIVE must be 0 or 1, not '$(ARCHIVE)')
endif

# generated/plain-nodes.yaml holds plain-3 and up (local-env.sh genesis
# writes it, empty for PLAIN_NODES up to 2).
COMPOSE_FILES := -f docker-compose.yaml -f generated/plain-nodes.yaml
COMPOSE := COMPOSE_PROFILES=$(PROFILES) docker compose -p perf-local $(COMPOSE_FILES)
# Every profile, so that stop and removal also reach the containers of a
# setting that is not the current one.
COMPOSE_ALL := COMPOSE_PROFILES=full,archive docker compose -p perf-local $(COMPOSE_FILES)
# Everything that belongs to one chain; `up` removes it (with anonymous
# volumes, which holds the archive database) before a new genesis. Plain
# nodes of a larger earlier PLAIN_NODES are orphans, which `up` and `down`
# remove too.
CHAIN := seed bp-1 bp-2 $(shell seq -f plain-%g 1 $(PLAIN_NODES)) archive archive-db rosetta

# Runs local-env.sh in the Mina image as the calling user, so generated files
# stay owned by them.
LOCAL_ENV := docker run --rm -u $(shell id -u):$(shell id -g) \
	-e MINA_PRIVKEY_PASS=$(LOCAL_KEY_PASS) -e OUT=/out -e TOPOLOGY=$(TOPOLOGY) -e ARCHIVE=$(ARCHIVE) \
	-e ITN_HARNESS=$(ITN_HARNESS) -e PLAIN_NODES=$(PLAIN_NODES) \
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
# build, hence the first `genesis` when it is missing. Only the images of
# the current setting are pulled, and only when missing, so that a locally
# built MINA_IMAGE (for example a daemon under test) works.
images: keys
	@[ -f ../../internal-trace-consumer/Dockerfile ] || { echo "the internal-trace-consumer submodule is missing; run: git submodule update --init internal-trace-consumer" >&2; exit 1; }
	@[ -f generated/nodes.env ] && [ -f generated/plain-nodes.yaml ] || $(LOCAL_ENV) genesis
	$(COMPOSE) pull --ignore-buildable --policy missing --quiet
	$(COMPOSE) build

# Every `up` starts a new chain: the genesis timestamp is fresh, and the
# nodes are recreated without state. All nodes (and the archive) are
# removed first, so nothing of another setting stays on the old chain. The
# genesis comes first because it writes generated/plain-nodes.yaml, which
# must name every plain node of this PLAIN_NODES before compose removes
# them; it takes seconds, well within the genesis delay. The perf-testing
# Postgres keeps its data; `make clean` drops it.
up: images
	$(LOCAL_ENV) genesis
	$(COMPOSE_ALL) rm -sfv $(CHAIN)
	$(COMPOSE) up -d --force-recreate --remove-orphans
	@echo "genesis: $$(grep GENESIS_TIMESTAMP generated/nodes.env | cut -d= -f2), topology: $(TOPOLOGY), plain nodes: $(PLAIN_NODES), archive: $(ARCHIVE)"
	@echo "orchestrator http://localhost:9090  dashboard http://localhost:4200"
	@[ "$(ARCHIVE)" = 0 ] || echo "rosetta http://localhost:3087  archive db postgres://postgres:local@localhost:55434/archive"

down:
	$(COMPOSE_ALL) down --remove-orphans

status:
	@$(COMPOSE_ALL) ps --format 'table {{.Service}}\t{{.Status}}'
	@$(COMPOSE) run --rm -T tools scripts/smoke.sh status

smoke:
	$(COMPOSE) run --rm -T tools scripts/smoke.sh run

logs:
	$(COMPOSE_ALL) logs -f --tail 50

clean:
	$(COMPOSE_ALL) down -v --remove-orphans
	rm -rf generated
