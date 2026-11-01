BINARY := chare

.PHONY: build run test fmt vet clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BINARY) ./cmd/chare

run:
	go run ./cmd/chare -data ./data

test:
	go test ./... -race -count=1

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
