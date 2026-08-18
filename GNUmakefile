default: build

.PHONY: build
build:
	go build -v ./...

.PHONY: install
install:
	go install -v ./...

.PHONY: test
test:
	go test -v -cover -timeout=120s ./...

# Acceptance tests drive the real Terraform CLI, which must be on PATH. They stub
# the Sentinel CLI out, so Sentinel is not needed for them.
#
# The integration tests in internal/sentinel are the ones that need the real
# `sentinel` binary; they skip when it is missing, under `make test` too.
.PHONY: testacc
testacc:
	TF_ACC=1 go test -v -cover -timeout=300s ./...

.PHONY: fmt
fmt:
	gofmt -s -w -e .

.PHONY: vet
vet:
	go vet ./...

# Same linter the CI runs. Install it with:
#   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
.PHONY: lint
lint:
	golangci-lint run
