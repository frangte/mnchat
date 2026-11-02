.PHONY: up db replica-status api search models web test load chaos-search proto down reset-db \
	k8s-up k8s-forward k8s-status k8s-down

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

# Local Kubernetes in Colima's k3s (colima start --kubernetes). Every kubectl call names the
# context, so nothing here can reach another cluster in your kubeconfig.
KUBE_CONTEXT ?= colima
KUBECTL = kubectl --context $(KUBE_CONTEXT)

k8s-up: ## build images and deploy to local k3s; then make k8s-forward and make web
	docker build -t mnchat-api:dev .
	docker build -t mnchat-search:dev -f Dockerfile.search .
	$(KUBECTL) apply -k deploy
	@# The tags stay :dev, so restart api and search to run the images just built.
	$(KUBECTL) -n mnchat rollout restart deploy/api deploy/search
	$(KUBECTL) -n mnchat rollout status statefulset/mysql --timeout=300s
	$(KUBECTL) -n mnchat rollout status statefulset/mysql-replica --timeout=300s
	$(KUBECTL) -n mnchat wait --for=condition=available deploy --all --timeout=300s
	@echo "Up. Next: make k8s-forward, then make web"

k8s-forward: ## forward api :8080, Grafana :3000, Prometheus :9090 to localhost; Ctrl-C stops
	KUBE_CONTEXT=$(KUBE_CONTEXT) ./scripts/k8s-forward.sh

k8s-status: ## pods, services and volumes in namespace mnchat
	$(KUBECTL) -n mnchat get pods,svc,pvc

k8s-down: ## delete namespace mnchat and everything in it, databases included
	$(KUBECTL) delete -k deploy --ignore-not-found
