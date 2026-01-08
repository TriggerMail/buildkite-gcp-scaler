# Stage 1: Download dependencies
FROM us-central1-docker.pkg.dev/bluecore-ops/dockerfiles/golang:lint-1.22 AS go-mod
WORKDIR /app
ENV GOPRIVATE=github.com/TriggerMail

# Configure git for private repos
RUN git config --global url."git@github.com:".insteadOf "https://github.com/" && \
    mkdir -p -m 0600 ~/.ssh && \
    ssh-keyscan github.com >> ~/.ssh/known_hosts

COPY go.mod go.sum ./
RUN --mount=type=ssh go mod download

# Stage 2: Vendor dependencies
FROM go-mod AS vendor
COPY pkg ./pkg
COPY scaler ./scaler
COPY main.go ./
RUN --mount=type=ssh go mod vendor

# Stage 3: Run tests
FROM vendor AS tests
RUN go test -v ./... -cover

# Stage 4: Run linter
FROM vendor AS lint
RUN go fmt ./...
RUN go vet ./...

# Stage 5: Build binary
FROM vendor AS build
RUN go build -mod=vendor -o bin/buildkite-gcp-autoscaler main.go

# Stage 6: Final runtime image
FROM alpine:latest AS runtime
RUN apk --no-cache add ca-certificates git openssh-client
WORKDIR /
COPY --from=build /app/bin/buildkite-gcp-autoscaler /buildkite-gcp-autoscaler
ENTRYPOINT ["/buildkite-gcp-autoscaler"]

# Default target for docker build
FROM runtime
