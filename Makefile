.PHONY: all build test vet clean

all: test build

build:
	go build -o specdot ./cmd/specdot

test:
	go test ./...

vet:
	go vet ./...
	go run ./cmd/specdot vet --profile build

clean:
	$(RM) specdot
