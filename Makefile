BUILDFLAGS := -trimpath

.PHONY: build
build:
	CGO_ENABLED=0 go build -trimpath -o directorius cmd/directorius/main.go

.PHONY: gen_mocks
gen_mocks:
	$(info * generating mock code)
	mockgen -package mocks -source internal/autoupdate/autoupdate.go -destination internal/autoupdate/mocks/autoupdate.go
	mockgen -package mocks -source internal/githubclt/client.go -destination internal/autoupdate/mocks/githubclient.go
	mockgen -package mocks -destination internal/autoupdate/mocks/ciclient.go github.com/simplesurance/directorius/internal/autoupdate CIClient

.PHONY: check
check:
	$(info * running static code checks)
	@golangci-lint run

.PHONY: test
test:
	$(info * running tests)
	go test -timeout=20s -race ./...
