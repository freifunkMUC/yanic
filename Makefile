all: coverage.print

## testing

.PHONY: coverage.out
coverage.out:
	go test -race -covermode=atomic -coverprofile=$@ ./...

coverage.print: coverage.out
	go tool cover -func $<

coverage.html: coverage.out
	go tool cover -html $< -o $@

.PHONY: test
test: coverage.out ## runs tests
