TEST_DATABASE_URL ?= postgres://test:test@localhost:5433/testdb?sslmode=disable
export TEST_DATABASE_URL

dev-up:
	docker compose up --build -d

dev-down:
	docker compose down

dev-logs:
	docker compose logs -f api

test-db-up:
	docker compose -f docker_compose.test.yml up -d

test-db-down:
	docker compose -f docker_compose.test.yml down -v

test-migrate:
	migrate \
		-path migrations \
		-database "$(TEST_DATABASE_URL)" \
		up

test-integration:
	go test ./... -count=1 -v

integration: test-db-up test-migrate test-integration
