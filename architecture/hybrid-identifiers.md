# Hybrid identifiers: BIGINT engine keys and UUIDv7 global IDs

## Target

| Entity | Operational identity | Global identity |
|---|---|---|
| Deployment | BIGINT PK | UUIDv7 UNIQUE |
| Process definition | BIGINT PK | UUIDv7 UNIQUE |
| Process instance | BIGINT PK | UUIDv7 UNIQUE |
| Published message | BIGINT PK | UUIDv7 UNIQUE |
| Execution, job, task, timer, incident, variable | BIGINT PK | none |

All engine foreign keys and Zeebe/REST keys use the BIGINT identity. UUIDv7
exists only on aggregate roots that cross database, backup or cluster
boundaries.

## Key generation

`internal/ids` generates UUIDv7 global IDs and positive 63-bit engine keys.
The engine-key layout is 41 timestamp bits, 10 node bits and 12 sequence bits.
Every concurrently running node must have a unique `ENGINE_NODE_ID` (0..1023).

## Implemented migration

1. Migration 009 adds and backfills the identities. Existing `zeebe_keys`
   values are reused, preserving every key already returned to a client.
2. Migration 010 promotes `engine_key` to the physical primary key of all ten
   migrated tables. Legacy UUID columns remain unique rolling-upgrade shadows;
   they are no longer authoritative keys.
3. Migration 011 adds, backfills and constrains the BIGINT relationship graph.
   Triggers dual-write it from legacy UUID writes while old binaries coexist.
4. New roots are generated with UUIDv7 and a Snowflake-style BIGINT. Existing
   root UUIDs are preserved byte-for-byte rather than silently renumbered.
5. gRPC and `/v2` resolve migrated resources directly through `engine_key`;
   `zeebe_keys` remains only as a fallback for non-entity keys such as signals
   and ephemeral messages.
6. Migration 013 switches repositories and physical foreign keys to BIGINT,
   then removes the UUID identity/relation shadows from hot tables. Migration
   016 does the same for `gateway_state`.
7. Migration 017 removes the rolling-upgrade identity triggers, the duplicate
   root `uid` columns and the unused `executions.gateway_join_id`. The final
   root shape is exactly `engine_key BIGINT PRIMARY KEY` plus one public
   `id UUID UNIQUE`; hot entities expose only `engine_key BIGINT`.

The `variables_notify` trigger is intentionally retained on plain PostgreSQL:
it is an application notification for conditional events, not an identifier
compatibility trigger. Citus drops it before distributing the table and sends
the notification from the repository instead.

Each application node must use a different `ENGINE_NODE_ID`. Reusing a node ID
can create duplicate keys when two nodes generate more than one key in the same
millisecond.
