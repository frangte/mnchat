.PHONY: db replica-status api web test proto down reset-db

db: ## start MySQL primary and read-only replica in docker
	docker-compose up -d mysql mysql-replica

replica-status: ## show replication health
	docker-compose exec -T mysql-replica mysql -uroot -proot -E -e "SHOW REPLICA STATUS" 2>/dev/null \
		| grep -E 'Replica_IO_Running:|Replica_SQL_Running:|Seconds_Behind_Source:|Last_(IO_|SQL_)?Error:'

api: ## run the Go API locally (applies migrations + demo seed)
	go run ./cmd/mnchat api

web: ## run the React dev server on :5173
	cd web && npm install && npm run dev

test:
	go vet ./... && go test ./...

proto: ## regenerate gRPC code from proto/
	protoc -I proto \
		--go_out=. --go_opt=module=github.com/frangte/mnchat \
		--go-grpc_out=. --go-grpc_opt=module=github.com/frangte/mnchat \
		proto/search/v1/search.proto

down:
	docker-compose down

reset-db: ## wipe MySQL data (re-seeds on next `make api`)
	docker-compose down -v
