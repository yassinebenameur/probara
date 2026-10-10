# Prometheus query monitors

Select **Prometheus** in the monitor picker under Infrastructure. Provide the
Prometheus base URL (including a reverse-proxy prefix, if any), a PromQL query,
and a healthy threshold. The worker POSTs to `/api/v1/query`, using the
[Prometheus instant-query API](https://prometheus.io/docs/prometheus/latest/querying/api/#instant-queries).

A monitor is healthy only when **every returned float sample** satisfies the
comparison (`lt`, `lte`, `gt`, `gte`, `eq`, `ne`). Use PromQL aggregation such as
`sum`, `max` or `min` to control how multiple series contribute. Thresholds are
expressed in the query's units. For example:

| Query | Healthy condition |
| --- | --- |
| `up{job="api"}` | Equal to 1 |
| `sum(queue_depth)` | Less than 1000 |
| `100 * sum(rate(http_requests_total{status=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))` | Less than 5 |

Avoid PromQL filtering comparisons such as `up == 0` unless an empty result is
intentional. A numeric query plus a Probara threshold is usually easier to
interpret. An empty vector defaults to **down**, with configurable **error** or
**up** behavior. NaN and infinity are errors, including division by zero.

Scalar and instant-vector float results are supported. Range vectors, strings,
native histogram samples, malformed responses, query warnings (potential partial
data), HTTP errors and connection failures report **error**. Existing monitor
state rules and the monitor's alert routing decide when failed checks become an alert.

## Authentication and network access

Use no authentication, Basic authentication, or a bearer token. Passwords and
bearer tokens use Probara's existing encryption and write-only masking. URLs
cannot contain credentials, query parameters or fragments. HTTPS certificates
are verified; redirects are rejected. The worker's existing SSRF policy and
allowed CIDRs apply, including DNS resolution at connection time. Select a
private location whose worker can reach your Prometheus endpoint when needed.

**Test query** runs an ephemeral check through a worker without saving the monitor.
When editing, saved masked credentials are resolved for that monitor. With
multiple locations, choose which location to preview. The result shows its
status, latency, sample count, failed count, and up to 20 numeric values. All
returned samples are evaluated, including those beyond the preview limit.
Responses are limited to 2 MiB; aggregate large queries instead of truncating
series. Labels and raw upstream errors are not stored in result metrics.

## API configuration

Create a monitor with `type: "prometheus"` and this config:

```json
{
  "url": "https://prometheus.example.com",
  "query": "sum(queue_depth)",
  "operator": "lt",
  "threshold": 1000,
  "no_data_status": "failure",
  "auth_type": "bearer",
  "bearer_token": "<supply securely>"
}
```

`threshold` is required; zero and negative finite values are valid. The query
is limited to 16 KiB. Basic authentication uses `username` and `password` instead
of `bearer_token`. Omitted or `***` secrets retain their stored value on update;
an explicit empty string clears them. Query syntax is validated by Prometheus
when the worker executes it, including during preview.

Deploy migration `000088` with the API and worker update; it extends the active
monitor timeout constraint. No new environment variables are required. Existing
alert routing, location quorum, status pages and import/export use the normal
monitor lifecycle. Remove Prometheus monitors before rolling back the database
migration, whose restored constraint does not accept this type.
