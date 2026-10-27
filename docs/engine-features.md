# Engine Features

What the GoFlow engine provides besides the BPMN elements ([BPMN 2.0 Support](bpmn-support.md)). Back to the [README](../README.md).

| Feature | Status |
|---|---|
| Process definition versioning | ✅ |
| Process variables (JSONB) | ✅ |
| FEEL expressions: native interpreter (`FEEL_MODE`, shadow by default) alongside the legacy FEEL-to-CEL path | ✅ |
| External task worker pattern (fetchAndLock) | ✅ |
| Job retries with exponential back-off | ✅ |
| Incident management: failed jobs, unhandled errors, and any failure on an element (condition, script, input collection, called process, timer…) as an incident on it; resolving it runs the element again (`POST /v2/incidents/:id/resolution`, gRPC `ResolveIncident`) | ✅ |
| Timer scheduler (SKIP LOCKED) | ✅ |
| Message correlation (`POST /engine-rest/message`) | ✅ |
| Signal broadcast (`POST /engine-rest/signal`) | ✅ |
| DMN decision engine (decision tables, standalone evaluation) | ✅ |
| Multi-tenancy (per-user tenant isolation) | ✅ |
| JWT authentication + RBAC | ✅ |
| OIDC bearer-token auth (optional, alongside JWT) | ✅ |
| Zeebe-style extensions (task headers, I/O mapping, FEEL assignee, form key) | ✅ |
| Forms (deploy + serve form schemas) | ✅ |
| Process instance modification ("Move Token") | ✅ |
| Previous step (send a token back to its earlier wait state) | ✅ |
| Audit trail (`audit_logs`, one row per state transition) | ✅ |
| Token-history replay (derived from the audit trail, see [API Reference](api.md#audit-trail--token-history-replay)) | ✅ |
| History API (process + activity instances) | ⚠️ basic — see [gaps](comparison.md#comparison-to-camunda-7--camunda-8) |
| SSE real-time events (`/events/tasks`) | ✅ |
| WebSocket real-time events (`/ws/tasks`) | ✅ |
| React frontend (Operate + Tasklist-lite) | ✅ |
| Docker / Docker Compose deployment | ✅ |
| Horizontal scaling: several instances behind nginx, PgBouncer, read replica, background archiving (see [Operations](operations.md#horizontal-scaling)) | ✅ |
| Postgres high availability: Patroni cluster, synchronous replication, automatic failover through HAProxy (see [Operations](operations.md#postgres-high-availability)) | ✅ |
| Backups (`pg_dump`, standalone or through the cluster) with a restore check | ✅ |
| gRPC / native Zeebe protocol | ✅ all 19 RPCs of the zeebe-node 8.3 client protocol (`MigrateProcessInstance`/`UpdateJobTimeout` from newer Zeebe versions not yet) |
| Partitioned write path, Camunda 8 style (`WRITE_PATH=partitioned`, see [Architecture](architecture.md#partitioned-write-path)) | ✅ |
