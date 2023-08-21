.DEFAULT_GOAL := lint

gofumpt:
	go install mvdan.cc/gofumpt@latest
	gofumpt -w .

lint:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin
	golangci-lint run --timeout 3m0s -E gofumpt ./...
