<h1 align="center">Kubernetes Events Manager</h1>

<p align="center">
  <img src="docs/assets/kem-icon-color.svg" width="256" alt="Kubernetes Events Manager" />
</p>

<p align="center">
  Kubernetes Events collection and forwarding agent
</p>

<p align="center"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue" />&nbsp;<img alt="Go 1.27 or later" src="https://img.shields.io/badge/go-1.27%2B-00ADD8" />&nbsp;<img alt="Kubernetes 1.27 or later" src="https://img.shields.io/badge/kubernetes-1.27%2B-326CE5" /></p>

---

> Collect Kubernetes Events from one or more namespaces, filter them with CEL expressions, and export them to the sinks you configure.

## Installation

Install the chart from the GitHub Container Registry:

```sh
helm install kem-agent oci://ghcr.io/shibernetes/charts/kem-agent \
  --namespace observability --create-namespace \
  --values values.yaml
```

The chart will not render until you declare at least one pipeline, so you cannot deploy a configuration that does nothing. RBAC permissions and the resources that go with them are inferred from the configuration: a `Role` when the agent only watches its own namespace, or a `ClusterRole` when it watches any other namespace, including all of them.

## Configuration overview

```yaml
source:
  watches:
    - namespace: payments
      field_selector: type=Warning
    - namespace: platform

sinks:
  collector:
    type: otel
    endpoint: collector:4317
  payments-alerts:
    type: webhook
    url: https://hooks.example.com/payments
    auth:
      bearer: ${PAYMENTS_WEBHOOK_TOKEN}

pipelines:
  archive:
    sinks:
      - collector
  pod-alerts:
    watches:
      - payments
    filters:
      - "event.type == 'Warning'"
      - "event.regarding.kind == 'Pod'"
    sinks:
      - payments-alerts

checkpoint:
  store:
    type: configmap
    name: kem-agent-checkpoint
```

To validate the config before using it, run the following command:

```sh
agent validate-config -c config.yaml
```

## Features

- **Six sinks** — Send events to OpenTelemetry (OTLP), HTTP webhooks, or Graylog, write them to a file or stdout, or turn them into Prometheus metric series.
- **CEL filters** — Filters are written in the [Common Expression Language](https://cel.dev/) (CEL), the same language Kubernetes uses for admission policies, so it should look familiar.
- **Routing** — One agent can processes several independent routes at once, each with its own namespaces, filters, and destinations.
- **Enrichment** — Attach the labels, annotations, and owner of the object an event refers to.
- **Sanitizers** — Cap labels and annotations, trim long annotation values, and drop specific annotations by name.
- **Checkpoints** — The agent records how far it has read. After a restart, it resumes from the last recorded position instead of replaying the full API server history.
- **Hot reload** — Filters can be updated without restarting the agent.

## Building from source

```sh
git clone git@github.com:shibernetes/kem-agent.git
cd kem-agent
go build ./cmd/...
```

## Known limitations

- Event delivery is at-most-once.
- Delivery queues are bounded and drop the oldest data when full, so a backend that stays down long enough loses its backlog rather than growing until the pod is killed.
- No durable queue storage, though it is planned.
- No leader election and no failover.

## License

[MIT](LICENSE)
