# Build the manager binary
FROM golang:1.12 as builder

# Install tools required to build the project.
# We need to run `docker build --no-cache .` to update those dependencies.
RUN go get github.com/golang/dep/cmd/dep

# Gopkg.toml and Gopkg.lock lists project dependencies.
# These layers are only re-built when Gopkg files are updated.
COPY Gopkg.lock Gopkg.toml /go/src/k8s-leader-elector/
WORKDIR /go/src/k8s-leader-elector

# Install library dependencies.
RUN dep ensure -vendor-only

# Copy src
COPY . .

# Build
ENV GOOS=linux
ENV GOARCH=amd64
RUN make build-no-lint

#
# runtime image
#
FROM ubuntu:18.04 AS runtime
WORKDIR /
RUN apt-get -y update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /go/src/k8s-leader-elector/k8s-leader-elector .
ENTRYPOINT ["/k8s-leader-elector"]
