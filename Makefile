.PHONY: all build test vet conform clean

all: test build conform

build:
	go build -o specdot ./cmd/specdot

test:
	go test ./...

vet:
	go vet ./...
	go run ./cmd/specdot vet --profile build

conform:
	go run ./cmd/specdot conform

clean:
	$(RM) specdot
