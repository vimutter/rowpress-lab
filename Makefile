COVERAGE_MIN ?= 100

.PHONY: test coverage coverage-check clean

test:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out

coverage-check:
	go test -coverprofile=coverage.out ./...
	@actual="$$(go tool cover -func=coverage.out | awk '/^total:/ { gsub(/%/, "", $$3); print $$3 }')"; \
	awk -v actual="$$actual" -v minimum="$(COVERAGE_MIN)" 'BEGIN { \
		printf "total coverage: %s%% (minimum: %s%%)\n", actual, minimum; \
		if (actual + 0 < minimum + 0) exit 1 \
	}'

clean:
	rm -f coverage.out coverage.html
