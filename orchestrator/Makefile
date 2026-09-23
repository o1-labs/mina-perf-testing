tidy:
	cd src && go mod tidy

src/graphql_generated.go: genqlient.graphql schema.graphql
	cd src && go get github.com/Khan/genqlient/generate@v0.5.0 && go run github.com/Khan/genqlient ../genqlient.yaml && cd ..

orchestrator: src/graphql_generated.go $(shell find src/ -type f)
	cd src/itn_orchestrator/ && go build -o ../../orchestrator && cd ..

orchestrator_service: src/graphql_generated.go $(shell find src/ -type f)
	cd src/orchestrator_service/ && go build -o ../../orchestrator_service && cd ..

generator: src/graphql_generated.go $(shell find src/ -type f)
	cd src/generator/ && go build -o ../../generator && cd ..

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
