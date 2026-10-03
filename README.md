<p align="center">
  <img src="docs/assets/kem-icon-color.svg" width="180" alt="Kubernetes Events Manager" />
</p>

<h1 align="center">Kubernetes Events Manager</h1>

<p align="center">
  Kubernetes Events collection and forwarding agent
</p>

<p align="center"><a href="https://github.com/shibernetes/kem-agent/actions/workflows/ci.yaml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/shibernetes/kem-agent/ci.yaml?label=CI" /></a>&nbsp;<a href="https://github.com/shibernetes/kem-agent/releases"><img alt="Latest release" src="https://img.shields.io/github/v/release/shibernetes/kem-agent?include_prereleases&sort=semver&label=Release" /></a>&nbsp;<img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/shibernetes/kem-agent?logo=go&logoColor=white&color=00ADD8" />&nbsp;<a href="https://pkg.go.dev/github.com/shibernetes/kem-agent"><img alt="Go Reference" src="https://img.shields.io/badge/go.dev-reference-007D9C?logo=go&logoColor=white" /></a>&nbsp;<img alt="Kubernetes 1.27 or later" src="https://img.shields.io/badge/Kubernetes-1.27%2B-326CE5?logo=kubernetes&logoColor=white" />&nbsp;<a href="https://artifacthub.io/packages/search?repo=kem-agent"><img alt="Artifact Hub" src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/kem-agent" /></a>&nbsp;<a href="https://scorecard.dev/viewer/?uri=github.com/shibernetes/kem-agent"><img alt="OpenSSF Scorecard" src="https://api.scorecard.dev/projects/github.com/shibernetes/kem-agent/badge" /></a>&nbsp;<img alt="License" src="https://img.shields.io/github/license/shibernetes/kem-agent?label=License" /></p>

---

> Kubernetes keeps Events for only an hour by default[^event-ttl]. This agent collects them cluster-wide or from the namespaces you choose, filters them with [CEL](https://cel.dev/) rules, and forwards them to your observability backends or counts them as Prometheus metrics.

> [!NOTE]
> The project is in alpha. Until version 1.0, the configuration format is subject to change between releases, and the [release notes](https://github.com/shibernetes/kem-agent/releases) describe each change.

## Table of contents

- [Features](#features)
- [Requirements](#requirements)
- [Release artifacts](#release-artifacts)
- [Install](#install)
  - [Helm chart](#helm-chart)
  - [Build from source](#build-from-source)
- [Configuration](#configuration)
  - [Sinks](#sinks)
  - [Filters](#filters)
  - [Validation](#validation)
- [Monitoring](#monitoring)
  - [Metrics](#metrics)
- [Known limitations](#known-limitations)
- [License](#license)

## Features

- **Sinks.** Send events to an OpenTelemetry collector over OTLP, to an HTTP webhook, or to Graylog. Write them to stdout or a file, or count them as Prometheus metrics.
- **CEL filters.** Keep only the events you care about, with [Common Expression Language](https://cel.dev/) expressions like the ones [Kubernetes admission policies](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/) use.
- **Pipelines.** Route different events to different backends from one agent, with pipelines that each have their own namespaces, filters, and sinks.
- **Enrichment.** Attach the labels, annotations, and owner of the object an event refers to, so that you can filter events by team, app, or owning workload.
- **Sanitizers.** Keep oversized events out of your backends by shortening long fields, capping labels and annotations, trimming long annotation values, and dropping kubectl's `last-applied-configuration` annotation.
- **Checkpoints.** Resume where the agent left off after a restart or an upgrade, with its progress saved in a ConfigMap or a file.
- **Hot reload.** Update filters without restarting the agent.

## Requirements

- Kubernetes 1.27 or later, with the [`WatchList` feature gate](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/#WatchList) enabled
- Helm 3.8 or later, to install the chart from an [OCI registry](https://helm.sh/docs/topics/registries/)

> [!IMPORTANT]
> The agent reads events with [streaming lists](https://kubernetes.io/docs/reference/using-api/api-concepts/#streaming-lists), which the API server serves only when the `WatchList` feature gate is enabled. The gate is enabled by default in Kubernetes 1.32 and in 1.34 or later. On 1.27 through 1.31 and on 1.33, [enable it in the API server](https://kubernetes.io/docs/tasks/administer-cluster/configure-feature-gates/). Without it, the agent stops at its first watch with an error saying that the API server doesn't serve initial events.
>
> To check a running cluster, look for `kubernetes_feature_enabled{name="WatchList"}` in the API server's `/metrics`.

## Release artifacts

Each [release](https://github.com/shibernetes/kem-agent/releases) publishes the following artifacts:

- The agent binary for Linux, on `amd64`, `arm/v7`, `arm64`, `ppc64le`, and `s390x`
- The agent binary for macOS, on `amd64` and `arm64`
- The [multi-architecture](https://oci.dag.dev/?image=ghcr.io/shibernetes/kem-agent:0.1.0-alpha.1) container image `ghcr.io/shibernetes/kem-agent`, for the same Linux platforms

Release images and the chart are signed with [cosign](https://github.com/sigstore/cosign). To check a signature, see [Verify the signature](deploy/chart/README.md#verify-the-signature).

## Install

### Helm chart

The chart reads the agent configuration from the `config` value. The following `values.yaml` is the smallest working setup. It prints every event in the release namespace to the agent's standard output.

```yaml
config:
  sinks:
    console:
      type: stdout
  pipelines:
    events:
      sinks:
        - console
```

Install the chart from GitHub Container Registry with those values.

```sh
helm install kem-agent oci://ghcr.io/shibernetes/charts/kem-agent \
  --namespace observability --create-namespace \
  --values values.yaml
```

To see the events, follow the agent logs.

```sh
kubectl logs -n observability deploy/kem-agent -f
```

When `config.source.watches` is empty, the chart adds a watch on the release namespace. The chart also creates the roles and role bindings that the agent needs, as described in [RBAC](deploy/chart/README.md#rbac). For the full list of chart values, see the [chart README](deploy/chart/README.md).

### Build from source

Building requires Go 1.27 or later. The following commands write the `agent` binary to the repository root.

```sh
git clone https://github.com/shibernetes/kem-agent.git
cd kem-agent
go build ./cmd/agent
```

Outside a pod, the binary uses your current kubeconfig context, or the kubeconfig file that the `--kubeconfig` flag points to.

## Configuration

A configuration has three main parts:

- The `source.watches` list sets the namespaces to read events from. A watch with no namespace reads all of them. A watch can also take a label or field selector, which the API server applies before it sends the events.
- The `sinks` map declares each destination once, by name.
- The `pipelines` map connects the two. Each pipeline reads from every watch, or only from the ones in its own `watches` list. It keeps the events that pass all of its `filters`, and sends them to its `sinks`.

A sink shared by several pipelines receives each event once, even when more than one of those pipelines selects it. Each watch and each sink must be referenced by at least one pipeline, or the configuration fails to load.

The following configuration reads only Warning events from `payments`, through a field selector, and every event from `platform`. It sends all of them to an OpenTelemetry collector, and the Warning events about pods to a webhook.

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

When the agent loads the configuration, it replaces `${PAYMENTS_WEBHOOK_TOKEN}` with the value of that environment variable, or fails to load if the variable isn't set. With the Helm chart, pass the variable from a Secret, as shown in [Secrets](deploy/chart/README.md#secrets).

With the `checkpoint` block, the agent saves its progress in the `kem-agent-checkpoint` ConfigMap, in its own namespace.

Hot reload is off by default. To apply filter changes without a restart, set `service.hot_reload` to `true`.

### Sinks

The `type` key of a sink selects where its events go.

| Type | Destination |
|---|---|
| `otel` | An OpenTelemetry collector, over [OTLP](https://opentelemetry.io/docs/specs/otlp/)/gRPC |
| `webhook` | An HTTP endpoint, as a JSON list, NDJSON, [CloudEvents](https://cloudevents.io/), or a custom body template |
| `graylog` | A Graylog [GELF](https://go2docs.graylog.org/current/getting_in_log_data/gelf.html) TCP input |
| `stdout` | The agent's standard output, one JSON event per line |
| `file` | A file, one JSON event per line |
| `metrics` | Prometheus counters, served on the agent's `/metrics` endpoint |

### Filters

A filter is a CEL expression on the `event` variable, which has the fields of an [`events.k8s.io/v1`](https://kubernetes.io/docs/reference/kubernetes-api/cluster-resources/event-v1/) Event, such as `type`, `reason`, `note`, `regarding`, and `series`. When `source.enrichment.resources` lists the resource an event refers to, `event.regardingObject` contains that object's labels, annotations, and owner. The following table shows a few common filters.

| Filter | Keeps |
|---|---|
| `event.reason in ['FailedScheduling', 'FailedMount']` | Scheduling and volume mount failures |
| `has(event.series) && event.series.count >= 5` | Events that repeated at least five times |
| `event.namespace.startsWith('team-')` | Events from namespaces whose name starts with `team-` |
| `event.?regardingObject.labels.team.orValue('') == 'payments'` | Events about objects labeled `team=payments`, with enrichment enabled |

A filter that reads a missing field or map key fails, and the pipeline drops the event. To read a field or key that might be missing, use the [`has()` macro](https://github.com/google/cel-spec/blob/master/doc/langdef.md#macros) or the [`.?` optional syntax](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypes), as the second and fourth examples do.

### Validation

To validate a configuration file before you deploy it, run the following command. It checks the configuration the same way the agent does at startup, without connecting to a cluster.

```sh
agent validate-config -c config.yaml
```

For completion and validation in your editor, use the auto-generated JSON schema in [`config/schema/agent.config.schema.json`](config/schema/agent.config.schema.json). With the [YAML language server](https://github.com/redhat-developer/yaml-language-server#using-a-modeline), add the following line at the top of the configuration file. To match the version you have deployed, replace `master` with a release tag.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/shibernetes/kem-agent/master/config/schema/agent.config.schema.json
```

## Monitoring

The agent serves Prometheus metrics at `/metrics` on `service.metrics_server.addr`, which defaults to `:8080`. It serves the liveness and readiness checks at `/healthz` and `/readyz` on a separate address, `service.http_server.addr`, which defaults to `:8081`.

To find out whether events are lost, check the `kem_agent_events_dropped_total` metric. Its `stage` and `reason` labels say where and why each event was dropped. When you deploy with the Helm chart, set `serviceMonitor.enabled` to `true` to create a Prometheus Operator [ServiceMonitor](https://prometheus-operator.dev/docs/api-reference/api/#monitoring.coreos.com/v1.ServiceMonitor), as described in [Metrics](deploy/chart/README.md#metrics).

### Metrics

For the full list of the agent's metrics, see [docs/metrics.md](docs/metrics.md).

## Known limitations

- Delivery is at-most-once. On a clean stop, the sinks have `service.shutdown_timeout` to deliver their pending events, and a crash loses them.
- Each sink has a bounded queue. When its backend stays down, the queue fills and then drops its oldest events, which caps memory use.
- Queues are kept in memory only. Durable queues are planned.
- The agent runs as a single replica. It has no leader election or failover.

## License

[MIT](LICENSE)

[^event-ttl]: The API server's `--event-ttl` flag sets how long Events are kept, and it defaults to one hour. See the [kube-apiserver reference](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-apiserver/).
