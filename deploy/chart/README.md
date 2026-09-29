# kem-agent

[![Latest release](https://img.shields.io/github/v/release/shibernetes/kem-agent?include_prereleases&sort=semver&label=Release)](https://github.com/shibernetes/kem-agent/releases)
![Kubernetes 1.27 or later](https://img.shields.io/badge/Kubernetes-1.27%2B-326CE5?logo=kubernetes&logoColor=white)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/kem-agent)](https://artifacthub.io/packages/search?repo=kem-agent)
[![License](https://img.shields.io/github/license/shibernetes/kem-agent?label=License)](https://github.com/shibernetes/kem-agent/blob/master/LICENSE)

---

Kubernetes Events collection and forwarding agent.

This chart deploys the [Kubernetes Events Manager agent](https://github.com/shibernetes/kem-agent),
which watches Kubernetes Events, filters them with
[CEL](https://cel.dev/) rules, sends them to OpenTelemetry, webhooks, Graylog,
or files, and turns them into Prometheus metrics. The
[project README](https://github.com/shibernetes/kem-agent#configuration)
describes the agent configuration, its sinks, and its filters.

## Requirements

- Kubernetes 1.27 or later, with the
  [`WatchList` feature gate](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/#WatchList)
  enabled
- Helm 3.8 or later, to install the chart from an
  [OCI registry](https://helm.sh/docs/topics/registries/)

> [!IMPORTANT]
> The agent needs [streaming lists](https://kubernetes.io/docs/reference/using-api/api-concepts/#streaming-lists)
> to read events, which the API server serves only when the [`WatchList`](https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/3157-watch-list/README.md)
> feature gate is enabled. The gate is on by default in Kubernetes 1.32 and in 1.34 or later. On 1.27 through
> 1.31 and on 1.33, [turn it on manually in the API server](https://kubernetes.io/docs/tasks/administer-cluster/configure-feature-gates/).
> If the feature gate is disabled, the agent fails when establishing watches with an error stating that the API server doesn't serve initial
> events.
>
> To check whether a running cluster has it on, read the API server's feature metric.
>
> `kubectl get --raw /metrics | grep '"WatchList"'`

## Install

The chart is published as an OCI artifact on GitHub Container Registry. Its
version matches the version of the agent image it deploys.

```bash
helm install kem-agent oci://ghcr.io/shibernetes/charts/kem-agent \
  --version <version> \
  --namespace kem-agent --create-namespace \
  --values values.yaml
```

The chart fails to render until `config` declares at least one pipeline. The
following values file is the smallest working setup. It sends every event in
the release namespace to stdout.

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

To check the install, wait for the rollout to finish, then follow the agent
logs. Each event is logged on stdout as a single JSON line.

```bash
kubectl rollout status -n kem-agent deploy/kem-agent
kubectl logs -n kem-agent deploy/kem-agent -f
```

## Verify the signature

The release workflow signs each chart version with cosign, using its GitHub
Actions identity rather than a key. The following command checks that a
version carries that signature. For the other verification options, see
[Verifying Signatures](https://docs.sigstore.dev/cosign/verifying/verify/) in
the Sigstore documentation.

```bash
cosign verify ghcr.io/shibernetes/charts/kem-agent:<version> \
  --certificate-identity-regexp '^https://github\.com/shibernetes/kem-agent/\.github/workflows/release\.yaml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Upgrade

To upgrade, run `helm upgrade` with the new version and your values file.

```bash
helm upgrade kem-agent oci://ghcr.io/shibernetes/charts/kem-agent \
  --version <version> \
  --namespace kem-agent \
  --values values.yaml
```

The Deployment uses the
[`Recreate` strategy](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#recreate-deployment),
so the old pod stops before the new one starts. The old pod saves its checkpoint before it exits, and the new
pod resumes from it, so the events written during the restart aren't lost.

Until version 1.0, the configuration format is subject to change between
releases. The [release notes](https://github.com/shibernetes/kem-agent/releases)
detail each change.

## Uninstall

To uninstall the release, run the following command.

```bash
helm uninstall kem-agent --namespace kem-agent
```

> [!NOTE]
> The agent creates the checkpoint ConfigMap itself, so Helm doesn't track it
> and leaves it in the cluster. For the `file` store, the chart annotates its
> PersistentVolumeClaim with
> [`helm.sh/resource-policy: keep`](https://helm.sh/docs/howto/charts_tips_and_tricks/#tell-helm-not-to-uninstall-a-resource),
> so `helm uninstall` doesn't delete it.
>
> A new install with the same release name finds the checkpoint and resumes
> where the previous one stopped. It doesn't resend the events that were already
> delivered, and it delivers the events created while the agent was uninstalled,
> as long as the API server still retains them.

To start from scratch, delete the checkpoint after you uninstall. The following
commands use the default names for the release name `kem-agent`.

```bash
# ConfigMap store
kubectl delete configmap -n kem-agent kem-agent-checkpoint
# File store on the chart's volume
kubectl delete pvc -n kem-agent kem-agent
```

## Configuration

The chart writes the `config` value to a ConfigMap mounted in the agent pod. It
passes the configuration through unchanged, except for the following:

- An empty `source.watches` list becomes a watch on the release namespace.
- A ConfigMap checkpoint store without a `name` is named
  `<fullname>-checkpoint`.
- Each `source.enrichment.resources` entry is an object with a `name` and an
  optional `clusterScoped` flag. The chart uses the flag when it creates the
  RBAC, because Helm can't look up a resource's scope. The agent receives
  only the names.

Changing `config` restarts the agent pod, because the chart adds a
[`checksum/config` annotation](https://helm.sh/docs/howto/charts_tips_and_tricks/#automatically-roll-deployments)
to the pod template. To turn off these restarts,
set `restartOnConfigChange` to `false`. The chart also leaves out the annotation
when `config.service.hot_reload` is `true`. Filter changes then apply without
a restart, and other changes take effect after a `kubectl rollout restart`.

The following sections show values for common setups. Merge them with a values
file that declares at least one pipeline, like the example in [Install](#install).

### Watched namespaces

The chart watches the release namespace unless `config.source.watches` lists
other namespaces. To watch every namespace, define a single watch with
`namespace: ""`. The chart creates the matching roles, as described in
[RBAC](#rbac).

```yaml
config:
  source:
    watches:
      - namespace: team-a
      - namespace: team-b
```

### Secrets

The chart stores `config` in a ConfigMap, which isn't meant for confidential
data. Keep credentials in a Secret instead, and expose them as environment
variables with a `secretKeyRef` in `env`, or a `secretRef` in `envFrom`. When
the agent loads its configuration, it replaces each `${VAR}` reference with the value
of that environment variable, or fails to start if the variable isn't set.

The following values read a webhook token from a Secret, as described in
[Define container environment variables using Secret data](https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/#define-container-environment-variables-using-secret-data).

```yaml
env:
  - name: WEBHOOK_TOKEN
    valueFrom:
      secretKeyRef:
        name: webhook-credentials
        key: token
config:
  sinks:
    alerts:
      type: webhook
      url: https://hooks.example.com/events
      auth:
        bearer: ${WEBHOOK_TOKEN}
  pipelines:
    alerts:
      sinks:
        - alerts
```

### Resource enrichment

Enrichment attaches the labels, annotations, and owner of the object an event
refers to. To turn it on, list the resources to enrich, such as `pods` or
`deployments.apps`. The chart grants the agent the `list` and `watch` permissions
for each of the declared resources.

A cluster-scoped resource, such as `nodes`, needs to be defined with the additional
`clusterScoped: true` property, because Helm can't discover a resource's scope and
the chart grants those through a ClusterRole.

> [!WARNING]
> The agent keeps an in-memory cache of every object of the declared resources
> in the watched namespaces, so its memory usage grows proportionally with the
> number of objects. Before you declare a resource with many objects, such as
> `pods`, on a large cluster, raise the memory limit as described in
> [Resources](#resources).

```yaml
config:
  source:
    enrichment:
      resources:
        - name: pods
        - name: deployments.apps
        - name: nodes
          clusterScoped: true
```

### TLS certificates

To use TLS certificates and keys stored in a Secret, mount it as a volume with
`extraVolumes` and `extraVolumeMounts`, and set the file paths in the sink's
`tls` settings. The agent reads the files when it starts, so restart the Deployment
after the Secret changes.

The following values configure an `otel` sink to connect to an OpenTelemetry
Collector over mTLS, with the CA, certificate, and key stored in the
`collector-tls` Secret.

```yaml
extraVolumes:
  - name: collector-tls
    secret:
      secretName: collector-tls
extraVolumeMounts:
  - name: collector-tls
    mountPath: /etc/kem-agent/tls
    readOnly: true
config:
  sinks:
    collector:
      type: otel
      endpoint: collector.observability:4317
      tls:
        ca_file: /etc/kem-agent/tls/ca.crt
        cert_file: /etc/kem-agent/tls/tls.crt
        key_file: /etc/kem-agent/tls/tls.key
  pipelines:
    collector:
      sinks:
        - collector
```

### Metrics

The agent serves Prometheus metrics on `/metrics`. With the Prometheus
Operator, set `serviceMonitor.enabled` to `true` to create a
[ServiceMonitor](https://prometheus-operator.dev/docs/api-reference/api/#monitoring.coreos.com/v1.ServiceMonitor),
and optionally set `serviceMonitor.labels` with the labels that your Prometheus
`serviceMonitorSelector` matches.

The following values create a ServiceMonitor for a Prometheus that selects the
`release: prometheus` label.

```yaml
serviceMonitor:
  enabled: true
  labels:
    release: prometheus
```

### Graceful shutdown

The chart sets the pod's termination grace period and startup probe from plain values,
not from `config`. If you raise one of the following agent timeouts, raise the pod
setting in the same row so that it stays above the agent timeout.

| Agent setting | Agent default | Pod setting | Pod default |
|---|---|---|---|
| `config.service.shutdown_timeout` | `30s` | `terminationGracePeriodSeconds` | 60 seconds |
| `config.source.enrichment.sync_timeout` | `1m` | `startupProbe.periodSeconds` × `startupProbe.failureThreshold` | 90 seconds |

If the
[grace period](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-termination)
is too short, the kubelet kills the agent before it can save the last
checkpoint, which causes the next pod to resume from an older position and
resend the events delivered since then.

If the [startup probe](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/#define-startup-probes)
gives up too early, the kubelet restarts the agent before it finishes syncing the
informer caches used for enrichment, which can lock it in a restart loop.

## RBAC

The chart creates the roles and role bindings that the agent pod needs, based on
`config`. Which ones it creates depends on how many namespaces the agent
watches.

| Watched namespaces | Objects created |
|---|---|
| One | Role and RoleBinding in the same namespace |
| Two or more | One ClusterRole, and a RoleBinding in each namespace |
| All (`namespace: ""`) | One ClusterRole and ClusterRoleBinding |

Cluster-scoped enrichment resources, such as `nodes` or `persistentvolumes`,
get a separate ClusterRole and ClusterRoleBinding.

To manage RBAC yourself, set `rbac.create` to `false`, and create the roles and
bindings in one of the following ways:

- With `extraObjects`, whose entries are rendered with `tpl` and can use the
  chart's ServiceAccount with the `kem-agent.serviceAccountName` template.
- In an
  [umbrella chart](https://helm.sh/docs/howto/charts_tips_and_tricks/#complex-charts-with-many-dependencies),
  with this chart as a dependency.

## Checkpoint storage

The default `configmap` store doesn't need a volume, but a `file` store does.
To use one, set `persistence.enabled` to `true` and set
`config.checkpoint.store.path` to a path under `persistence.mountPath`. To use
a claim that you created, set `persistence.existingClaim` to its name. Both stores
survive an uninstall, as described in [Uninstall](#uninstall).

## Resources

The default requests and limits are tailored for an agent with a few sinks and
no enrichment. The default CPU request handles roughly 1,400 events per second
through one sink.

Memory usage grows with the number of sinks and with the following factors. Set
`resources.limits.memory` above their total.

- The queue of each sink other than `metrics`, up to `queue.max_bytes` (`32Mi`
  by default)
- Two batches per sink other than `metrics`, each up to `batch.max_bytes`
  (`2Mi` by default for `otel`, `4Mi` for the others)
- The resource enrichment caches, about 800 bytes per cached object

> [!WARNING]
> A sink's queue grows while its backend is unreachable, so if the memory limit
> is too low, the container might be OOMKilled before the backend recovers.

## Security

The chart runs the agent as the nonroot user `65532`, with a read-only root
filesystem and all capabilities dropped. Also keep the following in mind:

- The HTTP port serves `/metrics`, the health probes, and `POST /reload`
  without authentication, and the chart's Service exposes it inside the
  cluster. To limit who can reach it, add a
  [NetworkPolicy](https://kubernetes.io/docs/concepts/services-networking/network-policies/),
  for example through `extraObjects`.
- Credentials belong in a Secret rather than in `config`, as shown in
  [Secrets](#secrets).
- The agent rejects a configuration that would send a header, token, or
  password over a connection without TLS.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| image.repository | string | `"ghcr.io/shibernetes/kem-agent"` | Agent image repository |
| image.tag | string | `""` | Image tag. Defaults to the chart's `appVersion` |
| image.digest | string | `""` | Image digest. When set, the image is pulled as `<repository>:<tag>@<digest>` |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| config | object | See values.yaml | Agent configuration, mounted in the agent pod from a ConfigMap. It must declare at least one pipeline. See [Configuration](#configuration) for what the chart fills in |
| restartOnConfigChange | bool | `true` | Restart the agent pod when `config` changes, using a `checksum/config` pod annotation. It has no effect when `config.service.hot_reload` is `true`, so that filter changes apply without a restart |
| terminationGracePeriodSeconds | int | `60` | Seconds the agent pod has to shut down before it's killed. Set it higher than `config.service.shutdown_timeout`, which defaults to `30s` |
| startupProbe.periodSeconds | int | `5` | How often the startup probe checks `/readyz`, in seconds |
| startupProbe.failureThreshold | int | `18` | Failed startup probes allowed before the container is restarted. `periodSeconds` × `failureThreshold` must be longer than `config.source.enrichment.sync_timeout`, which defaults to `1m`. The defaults allow 90 seconds |
| podSecurityContext | object | `{"fsGroup":65532,"runAsNonRoot":true,"runAsUser":65532,"seccompProfile":{"type":"RuntimeDefault"}}` | Security context for the agent pod. The user ID matches the image's nonroot user |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Security context for the agent container |
| resources | object | `{"limits":{"memory":"512Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | Resource requests and limits for the agent container |
| serviceAccount.create | bool | `true` | Create a ServiceAccount for the agent pod |
| serviceAccount.name | string | `""` | ServiceAccount name. Defaults to the chart's fullname, or to `default` when `create` is `false` |
| serviceAccount.annotations | object | `{}` | Annotations for the ServiceAccount |
| rbac.create | bool | `true` | Create the roles and role bindings the agent needs, based on `config`. See [RBAC](#rbac) |
| persistence.enabled | bool | `false` | Mount a PersistentVolumeClaim for a `file` checkpoint store or a `file` sink. The chart doesn't rewrite their paths, so point them under `mountPath` |
| persistence.mountPath | string | `"/var/lib/kem-agent"` | Path where the volume is mounted in the agent container |
| persistence.existingClaim | string | `""` | Existing PersistentVolumeClaim to use instead of creating one |
| persistence.size | string | `"1Gi"` | Size of the PersistentVolumeClaim the chart creates |
| persistence.storageClassName | string | `""` | Storage class of the PersistentVolumeClaim. Uses the cluster default when empty |
| persistence.accessModes | list | `["ReadWriteOnce"]` | Access modes of the PersistentVolumeClaim |
| service.annotations | object | `{}` | Annotations for the Service |
| serviceMonitor.enabled | bool | `false` | Create a Prometheus Operator ServiceMonitor that scrapes `/metrics` |
| serviceMonitor.interval | string | `"30s"` | Scrape interval. Uses the Prometheus default when empty |
| serviceMonitor.scrapeTimeout | string | `"10s"` | Scrape timeout. Uses the Prometheus default when empty |
| serviceMonitor.labels | object | `{}` | Extra labels for the ServiceMonitor, such as the ones your Prometheus uses to select it |
| podAnnotations | object | `{}` | Extra annotations for the agent pod |
| podLabels | object | `{}` | Extra labels for the agent pod |
| imagePullSecrets | list | `[]` | Secrets for pulling the image from a private registry |
| priorityClassName | string | `""` | Priority class for the agent pod |
| env | list | `[]` | Extra environment variables for the agent container. When the agent loads its configuration, it replaces each `${VAR}` reference in `config` with the value of that variable |
| envFrom | list | `[]` | ConfigMaps or Secrets to load environment variables from |
| extraVolumes | list | `[]` | Extra volumes for the agent pod. Mount them with `extraVolumeMounts` |
| extraVolumeMounts | list | `[]` | Extra volume mounts for the agent container, such as TLS files used by a sink |
| nodeSelector | object | `{}` | Node selector for the agent pod |
| tolerations | list | `[]` | Tolerations for the agent pod |
| affinity | object | `{}` | Affinity rules for the agent pod |
| extraObjects | list | `[]` | Extra manifests to deploy with the release, such as custom RBAC resources. Entries can be objects or strings. They're rendered with `tpl`, so they can use chart values and named templates |
| nameOverride | string | `""` | Override the chart name |
| fullnameOverride | string | `""` | Override the fullname used to name every object |
