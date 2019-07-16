.PHONY: docker-build docker-push
REPOSITORY_PREFIX ?= everpeace/
TAG ?= $(shell git rev-parse HEAD)

build:
	CGO_ENABLED=0 go build -a -o k8s-leader-elector

docker-build:
	docker build . -t $(REPOSITORY_PREFIX)k8s-leader-elector:$(TAG)

docker-push:
	docker push $(REPOSITORY_PREFIX)k8s-leader-elector:$(TAG)
