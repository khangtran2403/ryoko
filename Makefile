TEST_DB_URL=postgres://test:test@localhost:5433/testdb?sslmode=disable

test-db-up:
	docker compose -f docker_compose.test.yml up -d

test-db-down:
	docker compose -f docker_compose.test.yml down -v

test-migrate:
	migrate \
		-path migrations \
		-database "$(TEST_DB_URL)" \
		up

test-integration:
	go test ./... -count=1 -v

integration: test-db-up test-migrate test-integration