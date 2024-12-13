.PHONY: clean build test tidy docker-publish

ifeq ($(GO),)
GO := go
endif

clean:
	rm -rf result

build:
	GO=$(GO) ./build.sh

docker-publish:
	GO=$(GO) ./build.sh docker-publish

tidy:
	cd src && $(GO) mod tidy

test:
	GO=$(GO) ./build.sh test

