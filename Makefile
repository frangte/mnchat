.PHONY: db api web test down reset-db

db: ## start MySQL in docker
	docker-compose up -d mysql

api: ## run the Go API locally (applies migrations + demo seed)
	go run ./cmd/mnchat api

web: ## run the React dev server on :5173
	cd web && npm install && npm run dev

test:
	go vet ./... && go test ./...

down:
	docker-compose down

reset-db: ## wipe MySQL data (re-seeds on next `make api`)
	docker-compose down -v
