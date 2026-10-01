# Metrics

The agent serves the following metrics under the `/metrics` path, on the address that `service.metrics_server.addr` sets.

> [!WARNING]
> Until version 1.0, metric names and labels are subject to change, so every metric is marked `ALPHA`.

| Metric name | Metric type | Description | Labels/tags | Status |
|---|---|---|---|---|
| kem_agent_events_read_total | Counter | Events read from a watch for delivery. Deletions aren't counted. The watch over all namespaces reports `namespace="*"`. | `namespace`=&lt;watched-namespace&gt; | ALPHA |
| kem_agent_events_matched_total | Counter | Events that passed every filter of a pipeline. An event that several pipelines keep is counted once for each of them. | `pipeline`=&lt;pipeline-name&gt; | ALPHA |
| kem_agent_events_delivered_total | Counter | Events that a sink delivered. Records that the destination accepted and then refused aren't counted, such as those in an OTLP partial success. | `sink`=&lt;sink-name&gt; | ALPHA |
| kem_agent_events_dropped_total | Counter | Events lost before delivery. `stage` tells where an event was lost, and `reason` tells why, as listed in [Drop reasons](#drop-reasons). A filter drop sets only `pipeline`. Every other drop sets only `sink`. | `stage`=&lt;filter\|queue\|delivery\|sink&gt; <br> `reason`=&lt;drop-reason&gt; <br> `pipeline`=&lt;pipeline-name&gt; <br> `sink`=&lt;sink-name&gt; | ALPHA |
| kem_agent_send_duration_seconds | Histogram | Time taken by each delivery attempt, categorized by outcome. The wait between two retries isn't included. Buckets range from 5 milliseconds to 30 seconds. | `sink`=&lt;sink-name&gt; <br> `result`=&lt;success\|retryable\|permanent\|timeout\|oversized&gt; | ALPHA |
| kem_agent_queue_usage_bytes | Gauge | Bytes held in a sink's queue. A batch that the sink is filling or sending isn't included. The `metrics` sink has no queue. | `sink`=&lt;sink-name&gt; | ALPHA |
| kem_agent_queue_limit_bytes | Gauge | The `queue.max_bytes` limit of a sink's queue. Once the queue reaches it, the oldest events are dropped to make room for new ones. | `sink`=&lt;sink-name&gt; | ALPHA |
| kem_agent_watch_restarts_total | Counter | Watch reconnections, by reason. A `closed` restart means that the API server ended the watch without an error, usually when the timeout that the agent sets on each watch, between five and ten minutes, runs out. `error` is a failure retried with a backoff. `expired` means the resume position was too old, which is routine for a quiet namespace. | `namespace`=&lt;watched-namespace&gt; <br> `reason`=&lt;closed\|error\|expired&gt; | ALPHA |
| kem_agent_checkpoint_saves_total | Counter | Checkpoint writes, by result. `skipped` counts the writes left out because no position moved, so on a quiet cluster it grows while `success` stays flat. | `result`=&lt;success\|failure\|skipped&gt; | ALPHA |
| kem_agent_config_hot_reloads_total | Counter | Configuration reloads, by result. `partial` means the filters changed and the other changes require a restart. It only moves when `service.hot_reload` is `true`. | `result`=&lt;success\|partial\|failure&gt; | ALPHA |
| kem_agent_events_enriched_total | Counter | Enrichment lookups, by result, as listed in [Enrichment results](#enrichment-results). It only moves when `source.enrichment.resources` lists at least one resource. | `result`=&lt;enrichment-result&gt; | ALPHA |
| kem_agent_build_info | Gauge | Always 1. Its labels identify the running build. | `version`=&lt;agent-version&gt; <br> `commit`=&lt;git-commit&gt; <br> `go_version`=&lt;go-version&gt; | ALPHA |

> [!NOTE]
> The metrics that a `metrics` sink defines aren't listed, since they're user-defined. The agent also serves the Prometheus client library's Go runtime metrics, under `go_`, and its process metrics, under `process_`.

## Drop reasons

Each drop reason belongs to one stage of `kem_agent_events_dropped_total`.

| Stage | Reason | Cause |
|---|---|---|
| `filter` | `eval_error` | A filter expression failed, for example by reading a field that the event doesn't have. |
| `filter` | `eval_cost` | A filter expression, or all the expressions an event went through, reached a CEL cost limit (100,000 for one expression, 1,000,000 for one event). |
| `filter` | `eval_timeout` | Evaluating the event's filters took longer than `service.cel_eval_timeout`. |
| `queue` | `overflow` | The sink's queue was full, so its oldest event was dropped to make room. |
| `queue` | `too_large` | One event was larger than the sink's whole queue, as set by `queue.max_bytes`. |
| `delivery` | `retry_exhausted` | A batch kept failing until `retry.timeout` ran out, or until the agent stopped. With `retry.enabled` set to `false`, the first failure drops it. |
| `delivery` | `permanent_error` | The destination refused a batch for a reason that retrying can't fix, or because the batch was too large for it. |
| `sink` | `rejected` | The destination accepted the request and refused some of its records, or a `metrics` sink refused a new series over its `max_series` limit. |

## Enrichment results

| Result | Meaning |
|---|---|
| `hit` | The object the event refers to was found in the cache. |
| `miss` | The object wasn't in the cache, for example because it was deleted. |
| `undeclared` | The event refers to a resource that `source.enrichment.resources` doesn't list. |
| `cross_namespace` | The object is in a different namespace than the event, so the agent didn't look it up. |
| `skipped` | The event's reference has no kind or name to look up. |
