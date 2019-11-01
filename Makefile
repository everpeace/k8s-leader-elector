.PHONY: docker-build docker-push
REPOSITORY_PREFIX ?= everpeace/
VERSION      := $(if $(VERSION),$(VERSION),$(shell cat ./VERSION)-dev)
REVISION     := $(shell git rev-parse --short HEAD)
IMAGE_TAG    ?= $(VERSION)
LDFLAGS      := -ldflags="-s -w -X \"main.Version=$(VERSION)\" -X \"main.Revision=$(REVISION)\" -extldflags \"-static\""

build:
	CGO_ENABLED=0 go build -installsuffix netgo $(LDFLAGS) -a -o k8s-leader-elector

docker-build:
	docker build . -t $(REPOSITORY_PREFIX)k8s-leader-elector:$(IMAGE_TAG) --build-arg LDFLAGS=$(LDFLAGS)

docker-push:
	docker push $(REPOSITORY_PREFIX)k8s-leader-elector:$(IMAGE_TAG)
