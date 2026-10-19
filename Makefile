.PHONY: all build test vet conform clean

all: test build

build:
	go build -o gridspec ./cmd/gridspec

test:
	go test ./...

vet:
	go vet ./...
	go run ./cmd/gridspec vet --profile build

conform:
	go run ./cmd/gridspec conform

clean:
	$(RM) gridspec
