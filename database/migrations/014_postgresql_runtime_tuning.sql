-- 014_postgresql_runtime_tuning.sql
-- Runtime observability, polling indexes and per-table vacuum policy.
--
-- pg_stat_statements must also be present in shared_preload_libraries. The
-- Compose manifests do that; CREATE EXTENSION makes its views available.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- A UNIQUE index already serves every lookup on variables.process_instance_key.
DROP INDEX IF EXISTS public.idx_variables_instance_key;

-- Replace three overlapping timer indexes with the exact scheduler queue.
DROP INDEX IF EXISTS public.idx_timer_jobs_due_at;
DROP INDEX IF EXISTS public.idx_timer_jobs_due_triggered;
DROP INDEX IF EXISTS public.idx_timer_jobs_triggered;
CREATE INDEX IF NOT EXISTS idx_timer_jobs_pending_due
  ON public.timer_jobs (due_at, engine_key)
  INCLUDE (process_instance_key, execution_key)
  WHERE is_triggered = false;

-- Pending jobs are always fetched by type. created_at preserves FIFO order;
-- the included columns keep the claim scan on the index when visibility permits.
DROP INDEX IF EXISTS public.idx_jobs_status;
DROP INDEX IF EXISTS public.idx_jobs_type;
DROP INDEX IF EXISTS public.idx_jobs_type_status_pending;
CREATE INDEX IF NOT EXISTS idx_jobs_pending_type_created
  ON public.jobs (job_type, created_at, engine_key)
  INCLUDE (process_instance_key, execution_key, retries, locked_until)
  WHERE status = 'pending';

-- Incident resolution and duplicate detection only inspect open incidents.
DROP INDEX IF EXISTS public.idx_incidents_state;
CREATE INDEX IF NOT EXISTS idx_incidents_open_execution
  ON public.incidents (process_instance_key, execution_key)
  WHERE state = 'open';
CREATE INDEX IF NOT EXISTS idx_incidents_open_job
  ON public.incidents (job_key)
  WHERE state = 'open' AND job_key IS NOT NULL;

-- Update-heavy runtime tables need vacuum/analyze long before PostgreSQL's
-- generic 20%/10% defaults. Fillfactor leaves room for HOT updates.
ALTER TABLE public.jobs SET (
  fillfactor = 85,
  autovacuum_vacuum_scale_factor = 0.02,
  autovacuum_vacuum_threshold = 500,
  autovacuum_analyze_scale_factor = 0.01,
  autovacuum_analyze_threshold = 500,
  autovacuum_vacuum_cost_limit = 2000
);
ALTER TABLE public.executions SET (
  fillfactor = 90,
  autovacuum_vacuum_scale_factor = 0.03,
  autovacuum_vacuum_threshold = 500,
  autovacuum_analyze_scale_factor = 0.02,
  autovacuum_analyze_threshold = 500,
  autovacuum_vacuum_cost_limit = 2000
);
ALTER TABLE public.variables SET (
  fillfactor = 85,
  autovacuum_vacuum_scale_factor = 0.02,
  autovacuum_vacuum_threshold = 250,
  autovacuum_analyze_scale_factor = 0.01,
  autovacuum_analyze_threshold = 250,
  autovacuum_vacuum_cost_limit = 2000
);
ALTER TABLE public.audit_logs SET (
  autovacuum_vacuum_scale_factor = 0.05,
  autovacuum_vacuum_threshold = 1000,
  autovacuum_analyze_scale_factor = 0.02,
  autovacuum_analyze_threshold = 1000,
  autovacuum_vacuum_cost_limit = 2000
);

INSERT INTO public.schema_migrations (version, description)
VALUES ('014', 'pg_stat_statements, index partiels runtime et autovacuum ciblé')
ON CONFLICT (version) DO NOTHING;

COMMIT;
