BINARY := minos-worker

.PHONY: build test vet fmt clean up down logs smoke

build:
	go build -o bin/$(BINARY) ./cmd/worker

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin

# Docker dev stack
up:
	docker compose up -d --build

down:
	docker compose down -v

logs:
	docker compose logs -f worker

# End-to-end smoke test: push a python job, wait for the result
smoke:
	docker compose exec redis redis-cli RPUSH minos:queue \
	  '{"id":"smoke-1","language":"python","code":"print(42)"}'
	docker compose exec redis redis-cli BLPOP minos:result:smoke-1 15

# Full end-to-end suite: hello, timeout, exit codes, stderr, stdin, C, truncation
e2e:
	bash scripts/e2e-test.sh
