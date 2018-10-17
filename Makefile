.PHONY: dependencies test integration checks

all: dependencies checks test build

dependencies:
	dep ensure -v --vendor-only

test:
	go test -v -short ./...

integration:
	go test -v -timeout 5m ./...

build:
	go build .

checks:
	gometalinter --vendor --disable-all --enable=errcheck --enable=vet --enable=gofmt --enable=golint --enable=deadcode --enable=varcheck --enable=structcheck --enable=misspell --deadline=15m ./...