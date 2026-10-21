.PHONY: build test vet fmt clean

build:
	go build -o laserline ./cmd/laserline

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal

clean:
	rm -f laserline
