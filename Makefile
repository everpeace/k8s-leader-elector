REPOSITORY_PREFIX ?= everpeace/
VERSION      := $(if $(VERSION),$(VERSION),$(shell cat ./VERSION)-dev)
REVISION     := $(shell git rev-parse --short HEAD)
IMAGE_TAG    ?= $(VERSION)
LDFLAGS      := -ldflags="-s -w -X \"main.Version=$(VERSION)\" -X \"main.Revision=$(REVISION)\" -extldflags \"-static\""
.DEFAULT_GOAL := build

.PHONY: setup
setup:
	go get -u github.com/golangci/golangci-lint/cmd/golangci-lint

.PHONY: fmt
fmt:
	goimports -w ./*.go

.PHONY: lint
lint: fmt
	golangci-lint run --config .golangci.yml --deadline 30m

.PHONY: build
build: lint
	CGO_ENABLED=0 go build -installsuffix netgo $(LDFLAGS) -a -o k8s-leader-elector

.PHONY: docker-build docker-push
docker-build:
	docker build . -t $(REPOSITORY_PREFIX)k8s-leader-elector:$(IMAGE_TAG) --build-arg LDFLAGS=$(LDFLAGS)
docker-push:
	docker push $(REPOSITORY_PREFIX)k8s-leader-elector:$(IMAGE_TAG)
