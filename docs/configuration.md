# Environment Variables

Every variable can come either from the process environment or from a `.env` file (optional — real environment variables always win, and the Docker image no longer contains a `.env`). `docker-compose.yaml` reads secrets from `.env` next to it: without `JWT_SECRET_KEY` BpmnGo starts in demo mode (see below), `POSTGRES_PASSWORD` defaults to `goflow123`.

```bash
# Database
DB_HOST=postgres
DB_PORT=5432
DB_USER=goflow
DB_PASS=goflow123
DB_SSLMODE=disable   # or require / verify-ca / verify-full
DB_NAME=goflow

# JWT signing key. Generate with:
#   openssl rand -base64 48
# Set, the server runs in production mode and refuses known sample values.
# Unset, it runs in demo mode: the key is generated once and kept in
# DEMO_SECRET_FILE (default data/jwt_secret, the bpmngo_data volume in
# Docker) — to try BpmnGo, never for production.
JWT_SECRET_KEY=<random value>
# GOFLOW_ENV=production        # or demo; unset: production when JWT_SECRET_KEY
#                              # is set, demo otherwise. Production refuses a
#                              # sample JWT key, warns about an open /metrics
#                              # and the sample admin password
# DEMO_SECRET_FILE=data/jwt_secret

# Superuser (seeded on first boot)
# CREATE_SUPER_USER=false skips the seeding (unset = true)
CREATE_SUPER_USER=true
SUPER_USER_NAME=admin
SUPER_USER_EMAIL=admin@goflow.com
SUPER_USER_PASSWORD=admin123

# OIDC (optional) — set all three to accept bearer tokens from an external
# identity provider alongside the built-in JWT login; leave unset to disable
# OIDC_ISSUER=https://your-idp.example.com/realms/goflow
# OIDC_AUDIENCE=goflow-api
# OIDC_JWKS_URL=https://your-idp.example.com/realms/goflow/protocol/openid-connect/certs

# Kafka (Redpanda): only for the partitioned write path and the shadow event
# log. Direct mode (the default) needs no Kafka: leave KAFKA_BROKERS unset and
# Redpanda is not started. For WRITE_PATH=partitioned or EVENT_LOG=shadow, set
# KAFKA_BROKERS and start the "kafka" compose profile (COMPOSE_PROFILES=kafka).
# KAFKA_BROKERS=redpanda:9092
# EVENT_LOG=off                # or shadow: every engine event also written to
#                              # Kafka and projected; feeds only the
#                              # /engine-rest/eventlog/* diagnostic routes
# KAFKA_REPLICATION_FACTOR=1   # replicas per partition, applied when topics are created
# WRITE_PATH=direct            # or partitioned (Camunda 8 style, see above)
# KAFKA_PARTITIONS=12          # fixed once instances exist
# COMMAND_TIMEOUT=30s          # how long a request waits for its command

# Observability — bearer token required on GET /metrics (unset = open)
# METRICS_TOKEN=<random value>

# gRPC gateway (port 26500): every call needs credentials, as on REST —
# "authorization: Bearer <BpmnGo JWT or OIDC token>" or "Basic <e-mail:password>".
# Invalid credentials are always refused (UNAUTHENTICATED). true lets a call
# with NO credentials run as the first super_admin: trusted local networks only.
# GRPC_ALLOW_ANONYMOUS=false

# Retention — automatically delete instances that finished more than N days
# ago (unset or 0 = keep everything forever); see "Operations" below
# RETENTION_DAYS=365
# RETENTION_INTERVAL=1h
# RETENTION_BATCH_SIZE=500

# FEEL expressions ("=" prefix) and DMN tables: shadow (default: legacy
# FEEL-to-CEL path decides, native FEEL runs alongside and mismatches are
# logged), feel (native interpreter decides), cel (legacy only)
# FEEL_MODE=shadow

# Keys in /v2 (Camunda 8 API) answers: int by default (the gRPC gateway's
# int64 key, as a string as Camunda 8 does) or uuid for the legacy BpmnGo
# response format. /v2 accepts both forms in requests either way;
# /engine-rest (Camunda 7 API) stays on UUIDs.
# REST_KEYS=int

# Connection pool per BpmnGo instance (keep instances × this under
# Postgres's max_connections, or put PgBouncer in front)
# DB_MAX_OPEN_CONNS=80
# Top-level runtime admission limit; default 4 × GOMAXPROCS (minimum 4).
# On a two-CPU container, start with 8 and tune from
# goflow_runtime_admission_wait_seconds.
# RUNTIME_MAX_CONCURRENCY=8
# Minimum foreground concurrency retained above the hard archive backlog.
# Defaults to one quarter of RUNTIME_MAX_CONCURRENCY (minimum 1).
# RUNTIME_MIN_CONCURRENCY=2
# Halve admission at the soft backlog and use the minimum at the hard backlog.
# ARCHIVER_BACKLOG_SOFT_LIMIT=1000
# ARCHIVER_BACKLOG_HARD_LIMIT=5000
# POSTGRES_MAX_CONNECTIONS=200
# POSTGRES_SHARED_BUFFERS=256MB
# POSTGRES_EFFECTIVE_CACHE_SIZE=768MB
# POSTGRES_WORK_MEM=8MB
# POSTGRES_MAINTENANCE_WORK_MEM=128MB
# POSTGRES_MAX_WAL_SIZE=2GB
# PGBOUNCER_POOL_SIZE=80       # docker-compose.scale.yaml only
# In scale mode the manifest defaults each replica to 40 open / 10 idle
# client connections; PgBouncer multiplexes them onto PGBOUNCER_POOL_SIZE.

# Finished instances: archived in the background (async, default) or in the
# call that finishes them (sync)
# ARCHIVE_MODE=async

# Several instances (set by docker-compose.scale.yaml, see Operations):
# relay task events between instances for SSE/WebSocket clients
# TASK_EVENTS_FANOUT=true
# with PgBouncer in front of Postgres: where session-level work (LISTEN,
# advisory locks) reaches Postgres directly
# DB_DIRECT_HOST=postgres
# DB_DIRECT_PORT=5432
# read replica for history and analytics (unset = the primary)
# DB_READ_HOST=postgres-replica
# DB_READ_PORT=5432

# High-availability cluster (deploy/ha, see Operations): passwords of the
# Patroni nodes, the same on every node
# PG_SUPERUSER_PASSWORD=<random value>
# PG_STANDBY_PASSWORD=<random value>
# PG_ADMIN_PASSWORD=<random value>

# Citus-backed distributed storage (optional — see below)
# leave unset (the default) to run on a single plain Postgres instance;
# only set when using docker-compose.citus.yml, which sets both automatically
# CITUS_ENABLED=true
# CITUS_WORKERS=citus-worker-1:5432,citus-worker-2:5432
# CITUS_COORDINATOR_PORT=5433
```

`pg_stat_statements` is preloaded by every supplied PostgreSQL Compose
profile and migration 014 creates the extension. Query its cumulative view
after a representative load, for example:

```sql
SELECT calls, total_exec_time, mean_exec_time, rows, query
FROM pg_stat_statements
ORDER BY total_exec_time DESC
LIMIT 20;
```

Migration 015 partitions history and audit tables monthly and installs
`goflow_ensure_history_partitions(integer)`. BpmnGo calls it at startup to
keep twelve future months ready; DEFAULT partitions remain a safety net.

**Trying the Citus-backed deployment:**

```bash
docker compose -f docker-compose.yaml -f docker-compose.citus.yml up -d --build --wait
```

Brings up a Citus coordinator + 2 workers instead of the single Postgres instance; the plain `docker compose up` is unaffected. The per-instance tables — `process_instances`, `executions`, `tasks`, `variables`, `scope_variables`, `gateway_state`, `multi_instance_executions`, `incidents`, `jobs`, `timer_jobs` — are sharded over the workers, all colocated on the process instance, so an instance's rows live on one worker. Config tables are reference tables (copied to every node); the others stay on the coordinator as Citus local tables. Jobs and timers are claimed without `FOR UPDATE` (Citus forbids it across shards): candidates are read, then each is claimed by an update on its own shard that only succeeds while it is still free. See [`architecture/citus-distributed-storage-design.md`](../architecture/citus-distributed-storage-design.md).
