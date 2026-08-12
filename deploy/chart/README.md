# kem-agent

Kubernetes Events collection and forwarding agent

## Install

The chart is published to ghcr.io as an OCI artifact, at the same version as
the agent image it deploys.

```bash
helm install kem-agent oci://ghcr.io/shibernetes/charts/kem-agent \
  --version <version> \
  --namespace kem-agent --create-namespace \
  --values values.yaml
```

The chart renders nothing until `config` declares at least one pipeline. The
smallest working values write every event of the release namespace to stdout:

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

## Configuration

`config` holds the agent configuration, rendered into a ConfigMap mounted in
the pod. It is passed through as written, apart from three values the chart
fills in:

- an empty `source.watches` watches the release namespace
- a ConfigMap checkpoint store with no `name` uses `<fullname>-checkpoint`
- `source.enrichment.resources` entries are objects with a `name` and a
  `clusterScoped` field, but only the name is used by the agent

A configuration change restarts the pod through a `checksum/config` pod
annotation. With `config.service.hot_reload` enabled, the annotation is left
out, so filter changes apply in place and other changes need a rollout restart.

## RBAC

The chart grants what the configuration needs, from the watched namespaces:

| Watches | Events and namespaced enrichment resources |
|---|---|
| a single namespace | Role and RoleBinding in that namespace |
| two or more namespaces | ClusterRole, and a RoleBinding in each namespace |
| all namespaces (`namespace: ""`) | ClusterRole and ClusterRoleBinding |

Cluster-scoped enrichment resources take their own ClusterRole and
ClusterRoleBinding, and a ConfigMap checkpoint store takes a Role in its
namespace. Set `rbac.create` to `false` to manage RBAC yourself.

## Checkpoint storage

The default ConfigMap store needs no volume. A `file` store needs a volume at
`config.checkpoint.store.path`: set `persistence.enabled` and point the path
inside `persistence.mountPath`. The claim is kept when the release is
uninstalled.

## Verify the signature

The chart is signed with cosign by the release workflow:

```bash
cosign verify ghcr.io/shibernetes/charts/kem-agent:<version> \
  --certificate-identity-regexp '^https://github\.com/shibernetes/kem-agent/\.github/workflows/release\.yaml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Requirements

Kubernetes: `>=1.27.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| image.repository | string | `"ghcr.io/shibernetes/kem-agent"` | Image repository |
| image.tag | string | `""` | Image tag, defaults to the chart appVersion when empty |
| image.digest | string | `""` | Image digest, pinned alongside the tag when set |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| config | object | See values.yaml | The agent configuration, rendered into the mounted config file.  It passes through untouched apart from three things:  - an empty `source.watches` watches the release namespace  - a ConfigMap checkpoint store with no name uses `<fullname>-checkpoint`  - `source.enrichment.resources` entries are objects whose `name` is rendered  The chart does not render until at least one pipeline is declared. |
| restartOnConfigChange | bool | `true` | Restart the pod when the rendered config changes, through a `checksum/config` pod annotation. The annotation is left out when `config.service.hot_reload` is set to true, since it would restart the pod on every change and hot reload would never run. |
| terminationGracePeriodSeconds | int | `60` | Seconds the pod gets to stop. It must cover `config.service.shutdown_timeout` with headroom: the 30-second default plus 30 seconds. |
| startupProbe.periodSeconds | int | `5` | Seconds between the startup probes of `/readyz` |
| startupProbe.failureThreshold | int | `18` | Failed startup probes before the pod restarts. Times periodSeconds, it must cover `config.source.enrichment.sync_timeout` with a margin: 5 × 18 is 90 seconds, the 1-minute default plus 30 seconds. |
| podSecurityContext | object | `{"fsGroup":65532,"runAsNonRoot":true,"runAsUser":65532,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod security context. The numeric user matches the one the image runs as. |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container security context |
| resources | object | `{"limits":{"memory":"512Mi"},"requests":{"cpu":"100m","memory":"128Mi"}}` | Resource requests and limits. |
| serviceAccount.create | bool | `true` | Create the ServiceAccount the pod runs as |
| serviceAccount.name | string | `""` | ServiceAccount name. Defaults to the fullname, or to the namespace's `default` account when `create` is false. |
| serviceAccount.annotations | object | `{}` | ServiceAccount annotations |
| rbac.create | bool | `true` | Create RBAC resources, inferred from `config` |
| persistence.enabled | bool | `false` | Mount a PersistentVolumeClaim, which a `file` checkpoint store or a `file` sink writes to. The chart rewrites no path, so point those paths into `mountPath`. |
| persistence.mountPath | string | `"/var/lib/kem-agent"` | Where the volume is mounted in the agent container |
| persistence.existingClaim | string | `""` | Name of an existing PersistentVolumeClaim to mount rather than provisioning one |
| persistence.size | string | `"1Gi"` | Size of the provisioned volume |
| persistence.storageClassName | string | `""` | Storage class of the provisioned volume, the cluster default when empty |
| persistence.accessModes | list | `["ReadWriteOnce"]` | Access modes of the provisioned volume |
| service.annotations | object | `{}` | Service annotations |
| serviceMonitor.enabled | bool | `false` | Create a Prometheus Operator ServiceMonitor scraping `/metrics` |
| serviceMonitor.interval | string | `"30s"` | Scrape interval, the Prometheus default when empty |
| serviceMonitor.scrapeTimeout | string | `"10s"` | Scrape timeout, the Prometheus default when empty |
| serviceMonitor.labels | object | `{}` | Extra labels, such as the ones a Prometheus instance selects ServiceMonitors by |
| podAnnotations | object | `{}` | Pod annotations |
| podLabels | object | `{}` | Pod labels |
| imagePullSecrets | list | `[]` | Image pull secrets |
| priorityClassName | string | `""` | Priority class of the pod |
| env | list | `[]` | Extra environment variables. The agent replaces each `${VAR}` reference in `config` with the value of that variable when it loads the configuration. |
| envFrom | list | `[]` | Extra environment sources, whole ConfigMaps or Secrets |
| extraVolumes | list | `[]` | Extra pod volumes, mounted through `extraVolumeMounts` |
| extraVolumeMounts | list | `[]` | Extra volume mounts of the agent container, such as TLS files a sink reads |
| nodeSelector | object | `{}` | Node selector |
| tolerations | list | `[]` | Tolerations |
| affinity | object | `{}` | Affinity rules |
| extraObjects | list | `[]` | Extra manifests rendered beside the chart's own, such as custom RBAC. Each entry, an object or a string, is rendered through `tpl`, so it can use chart variables and helpers. |
| nameOverride | string | `""` | Override the chart name |
| fullnameOverride | string | `""` | Override the fully qualified release name |
