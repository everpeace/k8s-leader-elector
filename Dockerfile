# Build the manager binary
FROM golang:1.12 as builder

# LDFLAGS is defined as ARG(not ENV) because it is used only when build time.
ARG LDFLAGS

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
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o k8s-leader-elector

#
# runtime image
#
FROM ubuntu:18.04 AS runtime
WORKDIR /
COPY --from=builder /go/src/k8s-leader-elector/k8s-leader-elector .
ENTRYPOINT ["/k8s-leader-elector"]
