DBG ?= 0

ifeq ($(DBG),1)
GOGCFLAGS ?= -gcflags=all="-N -l"
endif

VERSION     ?= $(shell git describe --always --abbrev=7)
REPO_PATH   ?= github.com/openshift/cluster-api-provider-gcp
LD_FLAGS    ?= -X $(REPO_PATH)/pkg/version.Raw=$(VERSION) -extldflags -static

GO111MODULE = on
export GO111MODULE
GOFLAGS ?= -mod=vendor
export GOFLAGS
GOPROXY ?=
export GOPROXY

NO_DOCKER ?= 0
ifeq ($(NO_DOCKER), 1)
  DOCKER_CMD =
  IMAGE_BUILD_CMD = imagebuilder
  CGO_ENABLED = 1
else
  DOCKER_CMD := docker run --rm -e CGO_ENABLED=1 -v "$(PWD)":/go/src/github.com/openshift/cluster-api-provider-gcp:Z -w /go/src/openshift/cluster-api-provider-gcp openshift/origin-release:golang-1.13
  IMAGE_BUILD_CMD = docker build
endif

.PHONY: vendor
vendor:
	go mod tidy
	go mod vendor
	go mod verify

.PHONY: generate
generate:
	go generate ./pkg/... ./cmd/...

.PHONY: fmt
fmt: ## Go fmt your code
	hack/go-fmt.sh .

.PHONY: goimports
goimports: ## Go fmt your code
	hack/goimports.sh .

.PHONY: vet
vet: ## Apply go vet to all go files
	hack/go-vet.sh ./...

.PHONY: test
test: ## Run tests
	@echo -e "\033[32mTesting...\033[0m"
	$(DOCKER_CMD) hack/ci-test.sh

.PHONY: unit
unit: # Run unit test
	$(DOCKER_CMD) go test -race -cover ./cmd/... ./pkg/...

.PHONY: sec
sec: # Run security static analysis
	hack/gosec.sh ./...

.PHONY: build
build: ## build binaries
	$(DOCKER_CMD) go build $(GOGCFLAGS) -o "bin/machine-controller-manager" \
               -ldflags "$(LD_FLAGS)" "$(REPO_PATH)/cmd/manager"

.PHONY: test-e2e
test-e2e: ## Run e2e tests
	hack/e2e.sh
