tidy:
	cd src && go mod tidy

# BUILD is recorded by the service in every run (orchestrator_build); the
# Dockerfile passes its ORCHESTRATOR_BUILD argument here.
BUILD ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

orchestrator: $(shell find src/ -type f)
	cd src/itn_orchestrator/ && go build -o ../../orchestrator && cd ..

orchestrator_service: $(shell find src/ -type f)
	cd src/orchestrator_service/ && go build -ldflags "-X main.orchestratorBuild=$(BUILD)" -o ../../orchestrator_service && cd ..

generator: $(shell find src/ -type f)
	cd src/generator/ && go build -o ../../generator && cd ..

# src/graphql_generated.go is what genqlient used to write; an old copy would
# now clash with itn_inputs.go, so clean still removes it.
clean:
	rm -f generator orchestrator orchestrator_service src/graphql_generated.go

test:
	# -p 1: the DB-backed tests in service/ and orchestrator_service/ share one
	# database and one advisory lock, so parallel packages drop each other's
	# table and steal each other's lock. Harmless while TEST_PG_DSN is unset.
	cd src && go test -p 1 ./... && cd ..

vet:
	cd src && go vet ./... && cd ..

.PHONY: clean tidy test vet
