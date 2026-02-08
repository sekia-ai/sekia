BINARIES = sekiad sekiactl sekia-github sekia-slack sekia-linear sekia-gmail sekia-mcp
VERSION ?= dev
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: all build test vet clean docker

all: build

build:
	@for bin in $(BINARIES); do \
		echo "building $$bin"; \
		go build -ldflags "$(LDFLAGS)" -o $$bin ./cmd/$$bin; \
	done

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARIES)

docker:
	docker compose build
