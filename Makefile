.PHONY: up db replica-status api search models web test load chaos-search proto down reset-db

OLLAMA_CHAT_MODEL ?= gemma3:4b
OLLAMA_EMBED_MODEL ?= bge-m3
VUS ?= 5
DURATION ?= 60s

up: ## start the whole backend and monitoring stack in docker (the UI stays separate: make web)
	docker-compose up -d --build
	@echo "api http://localhost:8080 · Prometheus http://localhost:9090 · Grafana http://localhost:3000 · UI: make web"

db: ## start MySQL primary and read-only replica in docker
	docker-compose up -d mysql mysql-replica

replica-status: ## show replication health
	docker-compose exec -T mysql-replica mysql -uroot -proot -E -e "SHOW REPLICA STATUS" 2>/dev/null \
		| grep -E 'Replica_IO_Running:|Replica_SQL_Running:|Seconds_Behind_Source:|Last_(IO_|SQL_)?Error:'

api: ## run the Go API locally (applies migrations + demo seed)
	go run ./cmd/mnchat api

search: ## run the search service locally (gRPC :9190, metrics :9191)
	go run ./cmd/mnchat-search

models: ## pull the Ollama models search uses (about 4.6 GB on disk)
	ollama pull $(OLLAMA_CHAT_MODEL)
	ollama pull $(OLLAMA_EMBED_MODEL)

web: ## run the React dev server on :5173
	cd web && npm install && npm run dev

test:
	go vet ./... && go test ./...

load: ## k6 chat load in docker, $(VUS) users for $(DURATION); fails on any chat error
	docker-compose --profile load run --rm -T -e VUS=$(VUS) -e DURATION=$(DURATION) k6 run /scripts/chat.js

chaos-search: ## stop search, then kill the replica, under chat load; fails if chat sees any error
	./scripts/chaos-search.sh

proto: ## regenerate gRPC code from proto/
	protoc -I proto \
		--go_out=. --go_opt=module=github.com/frangte/mnchat \
		--go-grpc_out=. --go-grpc_opt=module=github.com/frangte/mnchat \
		proto/search/v1/search.proto

down: ## stop every container, keeping the data
	docker-compose down

reset-db: ## wipe MySQL data (re-seeds on next `make api`)
	docker-compose down -v
