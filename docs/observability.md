# Observability

Enable the local diagnostics server explicitly:

```yaml
Diagnostics:
  Enable: true
  Listen: 127.0.0.1:8080
```

Endpoints:

- `/healthz`: process liveness;
- `/readyz`: all configured nodes are initialized;
- `/status`: sanitized node state;
- `/metrics`: Prometheus metrics.

For structured logs:

```yaml
Log:
  Level: info
  Format: json
```

Metrics use only low-cardinality labels. User IDs, email addresses, IP addresses, tokens, passwords, and private keys are not labels.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `xrayr_panel_requests_total` | counter | `panel`, `operation`, `result` | Panel API calls by endpoint and logical outcome (`success` or an error kind such as `rate_limited`). |
| `xrayr_last_sync_timestamp_seconds` | gauge | `panel`, `node_type` | Unix timestamp of the last successful node synchronization. |
| `xrayr_users` | gauge | `panel`, `node_type` | Configured users on the node. |
| `xrayr_traffic_report_failures_total` | counter | `panel` | Failed traffic reports. |
| `xrayr_config_reloads_total` | counter | `result` | Configuration reload attempts (`success`, `rollback`, `failed`). |
| `xrayr_certificate_days_remaining` | gauge | `node_type` | Remaining validity of the leaf certificate in days. |
| `xrayr_core_healthy` | gauge | `node_type` | `1` when the last core liveness probe succeeded, `0` when it failed. |
| `xrayr_core_probe_failures_total` | counter | `node_type` | Failed core liveness probes. |

`xrayr_core_healthy` staying at `0` means the node is no longer accepting
connections. After three consecutive failed probes XrayR exits so the service
manager restarts it; see `DisableCoreHeartbeat` if you would rather it stayed up.

