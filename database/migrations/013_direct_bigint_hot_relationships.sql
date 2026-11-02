-- 013_direct_bigint_hot_relationships.sql
-- Complete the rolling migration started in 009-012.
--
-- All runtime relationships now use BIGINT directly. Aggregate roots keep
-- their UUIDv7 public identity, while the six hot entity families no longer
-- carry UUID compatibility shadows:
--   executions, jobs, tasks, timer_jobs, incidents, variables.

BEGIN;

-- Supporting runtime tables must move before the hot UUID columns disappear.
ALTER TABLE public.event_subprocesses
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS parent_execution_key BIGINT;
ALTER TABLE public.event_subscriptions
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS execution_key BIGINT;
ALTER TABLE public.boundary_events
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS execution_key BIGINT;
ALTER TABLE public.multi_instance_executions
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS execution_key BIGINT;
ALTER TABLE public.multi_instance_children
  ADD COLUMN IF NOT EXISTS parent_execution_key BIGINT,
  ADD COLUMN IF NOT EXISTS child_execution_key BIGINT;
ALTER TABLE public.execution_variables
  ADD COLUMN IF NOT EXISTS execution_key BIGINT;
ALTER TABLE public.scope_variables
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS scope_key BIGINT;
ALTER TABLE public.audit_logs
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS task_key BIGINT;

UPDATE public.event_subprocesses s SET process_instance_key = pi.engine_key
FROM public.process_instances pi
WHERE pi.id = s.process_instance_id AND s.process_instance_key IS NULL;
UPDATE public.event_subprocesses s SET parent_execution_key = e.engine_key
FROM public.executions e
WHERE e.id = s.parent_execution_id AND s.parent_execution_key IS NULL;

UPDATE public.event_subscriptions s SET process_instance_key = pi.engine_key
FROM public.process_instances pi
WHERE pi.id = s.process_instance_id AND s.process_instance_key IS NULL;
UPDATE public.event_subscriptions s SET execution_key = e.engine_key
FROM public.executions e
WHERE e.id = s.execution_id AND s.execution_key IS NULL;

UPDATE public.boundary_events b SET process_instance_key = pi.engine_key
FROM public.process_instances pi
WHERE pi.id = b.process_instance_id AND b.process_instance_key IS NULL;
UPDATE public.boundary_events b SET execution_key = e.engine_key
FROM public.executions e
WHERE e.id = b.execution_id AND b.execution_key IS NULL;

UPDATE public.multi_instance_executions m SET process_instance_key = pi.engine_key
FROM public.process_instances pi
WHERE pi.id = m.process_instance_id AND m.process_instance_key IS NULL;
UPDATE public.multi_instance_executions m SET execution_key = e.engine_key
FROM public.executions e
WHERE e.id = m.execution_id AND m.execution_key IS NULL;

UPDATE public.multi_instance_children c SET
  parent_execution_key = p.engine_key,
  child_execution_key = ch.engine_key
FROM public.executions p, public.executions ch
WHERE p.id = c.parent_execution_id AND ch.id = c.child_execution_id
  AND c.parent_execution_key IS NULL;

UPDATE public.execution_variables v SET execution_key = e.engine_key
FROM public.executions e
WHERE e.id = v.execution_id AND v.execution_key IS NULL;

UPDATE public.scope_variables v SET process_instance_key = pi.engine_key
FROM public.process_instances pi
WHERE pi.id = v.process_instance_id AND v.process_instance_key IS NULL;
UPDATE public.scope_variables v SET scope_key = e.engine_key
FROM public.executions e
WHERE e.id = v.scope_id AND v.scope_key IS NULL;

UPDATE public.audit_logs a SET process_instance_key = pi.engine_key
FROM public.process_instances pi
WHERE pi.id = a.process_instance_id AND a.process_instance_key IS NULL;
UPDATE public.audit_logs a SET task_key = t.engine_key
FROM public.tasks t
WHERE t.id = a.task_id AND a.task_key IS NULL;

ALTER TABLE public.event_subprocesses ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.event_subscriptions ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.boundary_events ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.multi_instance_executions
  ALTER COLUMN process_instance_key SET NOT NULL,
  ALTER COLUMN execution_key SET NOT NULL;
ALTER TABLE public.multi_instance_children
  ALTER COLUMN parent_execution_key SET NOT NULL,
  ALTER COLUMN child_execution_key SET NOT NULL;
ALTER TABLE public.execution_variables ALTER COLUMN execution_key SET NOT NULL;
ALTER TABLE public.scope_variables
  ALTER COLUMN process_instance_key SET NOT NULL,
  ALTER COLUMN scope_key SET NOT NULL;

-- Timer start events do not belong to an instance until they fire.
ALTER TABLE public.timer_jobs ALTER COLUMN process_instance_key DROP NOT NULL;

CREATE INDEX IF NOT EXISTS idx_event_subprocesses_instance_key
  ON public.event_subprocesses(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_event_subscriptions_instance_key
  ON public.event_subscriptions(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_event_subscriptions_execution_key
  ON public.event_subscriptions(execution_key) WHERE consumed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_boundary_events_instance_activity_active
  ON public.boundary_events(process_instance_key, attached_to_id) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_multi_instance_execs_instance_key
  ON public.multi_instance_executions(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_multi_instance_execs_execution_key
  ON public.multi_instance_executions(execution_key);
CREATE INDEX IF NOT EXISTS idx_multi_instance_children_parent_key
  ON public.multi_instance_children(parent_execution_key);
CREATE INDEX IF NOT EXISTS idx_multi_instance_children_child_key
  ON public.multi_instance_children(child_execution_key);
CREATE INDEX IF NOT EXISTS idx_execution_variables_execution_key
  ON public.execution_variables(execution_key);
CREATE INDEX IF NOT EXISTS idx_scope_variables_instance_key
  ON public.scope_variables(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_audit_logs_instance_key
  ON public.audit_logs(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_audit_logs_task_key
  ON public.audit_logs(task_key);

-- New history rows use BIGINT hot identifiers. Existing archived UUIDs are
-- retained as nullable legacy evidence and receive stable local BIGINT keys.
ALTER TABLE public.historic_activity_instances
  ADD COLUMN IF NOT EXISTS execution_key BIGINT,
  ADD COLUMN IF NOT EXISTS task_key BIGINT;
ALTER TABLE public.historic_tasks ADD COLUMN IF NOT EXISTS engine_key BIGINT;
ALTER TABLE public.historic_incidents
  ADD COLUMN IF NOT EXISTS engine_key BIGINT,
  ADD COLUMN IF NOT EXISTS job_key BIGINT;

DROP TABLE IF EXISTS _historic_execution_keys;
CREATE TEMP TABLE _historic_execution_keys AS
SELECT execution_id, nextval('public.engine_keys_legacy_seq')::BIGINT AS engine_key
FROM (SELECT DISTINCT execution_id FROM public.historic_activity_instances WHERE execution_id IS NOT NULL) x;
UPDATE public.historic_activity_instances h SET execution_key = m.engine_key
FROM _historic_execution_keys m
WHERE h.execution_id = m.execution_id AND h.execution_key IS NULL;

UPDATE public.historic_tasks SET engine_key = nextval('public.engine_keys_legacy_seq')
WHERE engine_key IS NULL;
UPDATE public.historic_activity_instances a SET task_key = t.engine_key
FROM public.historic_tasks t
WHERE a.task_id = t.id AND a.task_key IS NULL;

DROP TABLE IF EXISTS _historic_job_keys;
CREATE TEMP TABLE _historic_job_keys AS
SELECT job_id, nextval('public.engine_keys_legacy_seq')::BIGINT AS engine_key
FROM (SELECT DISTINCT job_id FROM public.historic_incidents WHERE job_id IS NOT NULL) x;
UPDATE public.historic_incidents h SET
  engine_key = nextval('public.engine_keys_legacy_seq'),
  job_key = m.engine_key
FROM _historic_job_keys m
WHERE h.job_id = m.job_id AND h.engine_key IS NULL;
UPDATE public.historic_incidents SET engine_key = nextval('public.engine_keys_legacy_seq')
WHERE engine_key IS NULL;
DROP TABLE _historic_execution_keys;
DROP TABLE _historic_job_keys;

ALTER TABLE public.historic_tasks DROP CONSTRAINT IF EXISTS historic_tasks_pkey;
ALTER TABLE public.historic_tasks ALTER COLUMN id DROP NOT NULL;
ALTER TABLE public.historic_tasks ALTER COLUMN engine_key SET NOT NULL;
ALTER TABLE public.historic_tasks ADD CONSTRAINT historic_tasks_pkey PRIMARY KEY (engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_historic_tasks_legacy_id
  ON public.historic_tasks(id) WHERE id IS NOT NULL;

ALTER TABLE public.historic_incidents DROP CONSTRAINT IF EXISTS historic_incidents_pkey;
ALTER TABLE public.historic_incidents ALTER COLUMN id DROP NOT NULL;
ALTER TABLE public.historic_incidents ALTER COLUMN engine_key SET NOT NULL;
ALTER TABLE public.historic_incidents ADD CONSTRAINT historic_incidents_pkey PRIMARY KEY (engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_historic_incidents_legacy_id
  ON public.historic_incidents(id) WHERE id IS NOT NULL;

-- Replace legacy composite/UUID uniqueness with direct BIGINT uniqueness.
ALTER TABLE public.variables DROP CONSTRAINT IF EXISTS unique_process_instance_variables;
CREATE UNIQUE INDEX IF NOT EXISTS uq_variables_process_instance_key
  ON public.variables(process_instance_key);

ALTER TABLE public.scope_variables DROP CONSTRAINT IF EXISTS scope_variables_pkey;
ALTER TABLE public.scope_variables ADD CONSTRAINT scope_variables_pkey
  PRIMARY KEY (process_instance_key, scope_key);

ALTER TABLE public.multi_instance_executions DROP CONSTRAINT IF EXISTS multi_instance_executions_process_instance_id_fkey;
ALTER TABLE public.multi_instance_executions DROP CONSTRAINT IF EXISTS multi_instance_executions_pkey;
ALTER TABLE public.multi_instance_executions ADD CONSTRAINT multi_instance_executions_pkey PRIMARY KEY (id);

-- Correlations are runtime relationships too: retain the published message's
-- UUIDv7 only on its root row, not redundantly on every correlation.
ALTER TABLE public.published_message_correlations
  DROP CONSTRAINT IF EXISTS published_message_correlations_pkey,
  DROP CONSTRAINT IF EXISTS published_message_correlations_published_message_id_fkey;
ALTER TABLE public.published_message_correlations
  ADD CONSTRAINT published_message_correlations_pkey PRIMARY KEY (published_message_key, process_key);

-- Notifications still expose the public process UUID to LISTEN clients.
CREATE OR REPLACE FUNCTION public.goflow_notify_variables() RETURNS trigger AS $$
DECLARE instance_uid UUID;
BEGIN
  SELECT id INTO instance_uid FROM public.process_instances
  WHERE engine_key = NEW.process_instance_key;
  PERFORM pg_notify('goflow_variables', instance_uid::text);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- No old binary may run beyond this point: remove dual-write machinery.
DROP TRIGGER IF EXISTS trg_process_definitions_hybrid_relationships ON public.process_definitions;
DROP TRIGGER IF EXISTS trg_process_instances_hybrid_relationships ON public.process_instances;
DROP TRIGGER IF EXISTS trg_executions_hybrid_relationships ON public.executions;
DROP TRIGGER IF EXISTS trg_jobs_hybrid_relationships ON public.jobs;
DROP TRIGGER IF EXISTS trg_tasks_hybrid_relationships ON public.tasks;
DROP TRIGGER IF EXISTS trg_timer_jobs_hybrid_relationships ON public.timer_jobs;
DROP TRIGGER IF EXISTS trg_incidents_hybrid_relationships ON public.incidents;
DROP TRIGGER IF EXISTS trg_variables_hybrid_relationships ON public.variables;
DROP TRIGGER IF EXISTS trg_published_message_correlations_hybrid_relationships ON public.published_message_correlations;
DROP FUNCTION IF EXISTS public.fill_hybrid_relationship_keys();

-- Drop UUID FK constraints/indexes before their columns.
ALTER TABLE public.executions DROP CONSTRAINT IF EXISTS executions_process_instance_id_fkey;
ALTER TABLE public.tasks DROP CONSTRAINT IF EXISTS tasks_process_instance_id_fkey;
ALTER TABLE public.variables DROP CONSTRAINT IF EXISTS variables_process_instance_id_fkey;
ALTER TABLE public.incidents DROP CONSTRAINT IF EXISTS incidents_process_instance_id_fkey;

DROP INDEX IF EXISTS public.uq_executions_legacy_id;
DROP INDEX IF EXISTS public.uq_jobs_legacy_id;
DROP INDEX IF EXISTS public.uq_tasks_legacy_id;
DROP INDEX IF EXISTS public.uq_timer_jobs_legacy_id;
DROP INDEX IF EXISTS public.uq_incidents_legacy_id;
DROP INDEX IF EXISTS public.uq_variables_legacy_id;

-- Supporting tables first: they no longer depend on the UUID shadows.
ALTER TABLE public.event_subprocesses DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS parent_execution_id;
ALTER TABLE public.event_subscriptions DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS execution_id;
ALTER TABLE public.boundary_events DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS execution_id;
ALTER TABLE public.multi_instance_executions DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS execution_id;
ALTER TABLE public.multi_instance_children DROP COLUMN IF EXISTS parent_execution_id, DROP COLUMN IF EXISTS child_execution_id;
ALTER TABLE public.execution_variables DROP COLUMN IF EXISTS execution_id;
ALTER TABLE public.scope_variables DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS scope_id;
ALTER TABLE public.audit_logs DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS task_id;
ALTER TABLE public.published_message_correlations
  DROP COLUMN IF EXISTS published_message_id,
  DROP COLUMN IF EXISTS process_instance_id;

-- Hot entities: BIGINT only, exactly as the runtime model.
ALTER TABLE public.executions DROP COLUMN IF EXISTS id, DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS parent_execution_id;
ALTER TABLE public.jobs DROP COLUMN IF EXISTS id, DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS execution_id;
ALTER TABLE public.tasks DROP COLUMN IF EXISTS id, DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS execution_id;
ALTER TABLE public.timer_jobs DROP COLUMN IF EXISTS id, DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS execution_id;
ALTER TABLE public.incidents DROP COLUMN IF EXISTS id, DROP COLUMN IF EXISTS process_instance_id, DROP COLUMN IF EXISTS job_id, DROP COLUMN IF EXISTS execution_id;
ALTER TABLE public.variables DROP COLUMN IF EXISTS id, DROP COLUMN IF EXISTS process_instance_id;

INSERT INTO public.schema_migrations (version, description)
VALUES ('013', 'écriture directe BIGINT et suppression des UUID des entités chaudes')
ON CONFLICT (version) DO NOTHING;

COMMIT;
