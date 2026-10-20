.PHONY: build test vet fmt clean

build:
	go build -o warp ./cmd/warp

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal

clean:
	rm -f warp
