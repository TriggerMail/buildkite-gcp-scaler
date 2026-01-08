# buildkite-gcp-scaler

buildkite-gcp-scaler is a simple scaling utility for running buildkite jobs on
Google Cloud Platform. It is designed to be ran inside a periodic scheduler such
as Nomad.

It uses Unmanaged Instance Groups to allow for self-terminating single-use
instances in public cloud infrastructure.

Authentication is managed by default credentials in the Google Cloud Go SDK.

## Quick Start

### Build & Test

```bash
# Run all CI steps (tests, lint, docker build)
make ci

# Run tests only
make tests

# Run linter only
make lint

# Build Docker image
make docker

# Build binary locally
make build-local

# See all available commands
make help
```

### Usage

```bash
./buildkite-gcp-autoscaler run \
  --buildkite-token=$BUILDKITE_TOKEN \
  --buildkite-queue=default \
  --buildkite-cluster=$CLUSTER_ID \
  --org=myorg \
  --gcp-project=my-project \
  --gcp-zone=us-central1-a \
  --instance-group=buildkite-agents \
  --instance-template=agent-template \
  --interval=30s
```

### Cluster Support

The scaler supports both **clustered** and **unclustered** Buildkite agents:

- **For clustered agents**: Pass `--buildkite-cluster=<cluster-id>` to only scale agents for that specific cluster
- **For unclustered agents**: Omit the `--buildkite-cluster` flag (backward compatible with existing deployments)

This ensures multiple scalers can run simultaneously without interfering with each other.


## TODO

- [ ] Dynamic Token Generation with the GraphQL API. This is currently
      unimplemented as it's not _strictly_ necessary for my current use case,
      and because tokens currently have no way to be strictly single-use.

