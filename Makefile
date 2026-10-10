BINARY := inspector-gopher
PKG := ./cmd/inspector-gopher

.PHONY: build test vet lint run-demo docker-demo clean

build:
	go build -o $(BINARY) $(PKG)

test:
	go test ./... -race -count=1

vet:
	go vet ./...

lint:
	golangci-lint run ./...

run-demo:
	go run ./demo/pprof-demo

docker-demo:
	docker build -f demo/pprof-demo/Dockerfile -t pprof-demo:local .
	docker rm -f pprof-demo 2>/dev/null; docker run -d --name pprof-demo -p 6060:6060 pprof-demo:local

clean:
	rm -f $(BINARY)
	docker rm -f pprof-demo 2>/dev/null; true
