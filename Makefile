BIN := bin/pixbowl

.PHONY: build run test fmt vet docker clean

build:
	CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/pixbowl

run: build
	./$(BIN)

test:
	go test ./...

fmt:
	gofmt -w ./cmd ./internal

vet:
	go vet ./...

docker:
	docker build -t pixbowl:latest .

clean:
	rm -rf bin
