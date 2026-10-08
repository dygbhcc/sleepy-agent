.PHONY: test lint demo skeleton compare

test:
	go test -race ./...

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...

demo:
	go run ./cmd/demo

skeleton:
	go run ./cmd/skeleton

compare:
	go run ./cmd/compare
