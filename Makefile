.PHONY: build run test clean tidy

BINARY=filerename

build:
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/filerename

run:
	go run ./cmd/filerename

test:
	go test -v ./...

clean:
	rm -f $(BINARY) $(BINARY).exe

tidy:
	go mod tidy
	go mod download

install:
	go install -ldflags="-s -w" ./cmd/filerename

windows:
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY).exe ./cmd/filerename

linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY)-linux ./cmd/filerename

macos:
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o $(BINARY)-macos ./cmd/filerename
