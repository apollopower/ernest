BINARY := ernest

.PHONY: build run build-linux build-mac-arm build-mac-intel build-all clean

build:
	go build -o dist/$(BINARY) ./cmd/ernest

run: build
	./dist/$(BINARY)

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/$(BINARY)-linux-amd64 ./cmd/ernest

build-mac-arm:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/$(BINARY)-darwin-arm64 ./cmd/ernest

build-mac-intel:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o dist/$(BINARY)-darwin-amd64 ./cmd/ernest

build-all: build-linux build-mac-arm build-mac-intel

test:
	go test ./...

clean:
	rm -rf dist/
