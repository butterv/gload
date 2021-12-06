.PHONY: init
init:
	go mod download

.PHONY: lint
lint:
	staticcheck ./...

.PHONY: test
test:
	go test -v -race ./...
