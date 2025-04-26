BIN_DIR := ./bin
TAGS := sqlite_math_functions
LDFLAGS := -s -w
GOFLAGS := -trimpath

.PHONY: all build clean hn unl lint test fmt refresh tidy

all: build

build: hn unl

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

hn: | $(BIN_DIR)
	go build $(GOFLAGS) \
	         -ldflags "$(LDFLAGS)" \
	         -o $(BIN_DIR)/hn \
	         -tags $(TAGS) \
	         ./cmd/hn

unl: | $(BIN_DIR)
	go build $(GOFLAGS) \
	         -ldflags "$(LDFLAGS)" \
	         -o $(BIN_DIR)/unl \
	         -tags $(TAGS) \
	         ./cmd/unl

lint:
	golangci-lint run

test:
	go test -race ./... -tags $(TAGS)

bench:
	go test -run=^$$ -bench=. -benchmem ./... -tags $(TAGS)

fmt:
	go fmt ./... && gofumpt -w .

clean:
	rm -rf $(BIN_DIR)

refresh: hn
	./testdata/refresh.sh

tidy:
	go mod tidy
