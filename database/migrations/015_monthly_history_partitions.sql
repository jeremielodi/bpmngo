-- 015_monthly_history_partitions.sql
-- Convert append-only history and audit storage to monthly RANGE partitions.
--
-- Upgrade note: this migration takes ACCESS EXCLUSIVE locks while it copies
-- the existing rows. Apply it during a maintenance window. Retention already
-- deletes child rows explicitly, so removing the historic child FKs does not
-- change the application's cleanup behavior.

BEGIN;

ALTER TABLE public.historic_activity_instances
  DROP CONSTRAINT IF EXISTS historic_activity_instances_process_instance_id_fkey;
ALTER TABLE public.historic_tasks
  DROP CONSTRAINT IF EXISTS historic_tasks_process_instance_id_fkey;
ALTER TABLE public.historic_variables
  DROP CONSTRAINT IF EXISTS historic_variables_process_instance_id_fkey;
ALTER TABLE public.historic_incidents
  DROP CONSTRAINT IF EXISTS historic_incidents_process_instance_id_fkey;

ALTER TABLE public.historic_process_instances RENAME TO historic_process_instances_pre015;
ALTER TABLE public.historic_activity_instances RENAME TO historic_activity_instances_pre015;
ALTER TABLE public.historic_tasks RENAME TO historic_tasks_pre015;
ALTER TABLE public.historic_variables RENAME TO historic_variables_pre015;
ALTER TABLE public.historic_incidents RENAME TO historic_incidents_pre015;
ALTER TABLE public.audit_logs RENAME TO audit_logs_pre015;

CREATE TABLE public.historic_process_instances (
  id UUID NOT NULL,
  process_definition_id UUID NOT NULL,
  process_definition_key TEXT NOT NULL,
  process_definition_version INTEGER NULL,
  tenant_id TEXT NULL,
  started_by TEXT NULL,
  started_at TIMESTAMP NOT NULL,
  ended_at TIMESTAMP NOT NULL,
  duration_millis BIGINT NULL,
  state TEXT NOT NULL,
  delete_reason TEXT NULL,
  archived_at TIMESTAMP NOT NULL DEFAULT NOW(),
  business_key TEXT NULL,
  PRIMARY KEY (id, ended_at)
) PARTITION BY RANGE (ended_at);

CREATE TABLE public.historic_activity_instances (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  process_instance_id UUID NOT NULL,
  execution_id UUID NULL,
  action TEXT NOT NULL,
  element_id TEXT NULL,
  element_name TEXT NULL,
  task_id UUID NULL,
  detail TEXT NULL,
  occurred_at TIMESTAMP NOT NULL,
  execution_key BIGINT NULL,
  task_key BIGINT NULL,
  PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE public.historic_tasks (
  id UUID NULL,
  process_instance_id UUID NOT NULL,
  task_definition_key TEXT NOT NULL,
  task_name TEXT NULL,
  assignee TEXT NULL,
  status TEXT NOT NULL,
  priority INTEGER NULL,
  created_at TIMESTAMP NOT NULL,
  claimed_at TIMESTAMP NULL,
  completed_at TIMESTAMP NULL,
  engine_key BIGINT NOT NULL,
  PRIMARY KEY (engine_key, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE public.historic_variables (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  process_instance_id UUID NOT NULL,
  data JSONB NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (id, updated_at)
) PARTITION BY RANGE (updated_at);

CREATE TABLE public.historic_incidents (
  id UUID NULL,
  process_instance_id UUID NOT NULL,
  process_definition_key TEXT NOT NULL,
  job_id UUID NULL,
  incident_type TEXT NOT NULL,
  activity_id TEXT NOT NULL,
  error_message TEXT NULL,
  error_code TEXT NULL,
  state TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  resolved_at TIMESTAMP NULL,
  archived_at TIMESTAMP NOT NULL DEFAULT NOW(),
  engine_key BIGINT NOT NULL,
  job_key BIGINT NULL,
  PRIMARY KEY (engine_key, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE public.audit_logs (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  user_id UUID NULL REFERENCES public.users(id),
  action TEXT NOT NULL,
  old_data JSONB NULL,
  new_data JSONB NULL,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  process_instance_key BIGINT NULL,
  task_key BIGINT NULL,
  PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Creates [from_month, through_month] partitions for one known parent.
CREATE OR REPLACE FUNCTION public.goflow_create_month_partitions(
  parent_name TEXT,
  partition_prefix TEXT,
  from_month DATE,
  through_month DATE
) RETURNS void AS $$
DECLARE
  month_start DATE := date_trunc('month', from_month)::date;
  last_month DATE := date_trunc('month', through_month)::date;
  partition_name TEXT;
BEGIN
  IF parent_name NOT IN (
    'historic_process_instances', 'historic_activity_instances',
    'historic_tasks', 'historic_variables', 'historic_incidents', 'audit_logs'
  ) THEN
    RAISE EXCEPTION 'unsupported partition parent: %', parent_name;
  END IF;
  WHILE month_start <= last_month LOOP
    partition_name := partition_prefix || '_' || to_char(month_start, 'YYYYMM');
    EXECUTE format(
      'CREATE TABLE IF NOT EXISTS public.%I PARTITION OF public.%I FOR VALUES FROM (%L) TO (%L)',
      partition_name, parent_name, month_start::timestamp,
      (month_start + INTERVAL '1 month')::timestamp
    );
    IF parent_name = 'audit_logs' THEN
      EXECUTE format(
        'ALTER TABLE public.%I SET (autovacuum_vacuum_scale_factor=0.05, autovacuum_vacuum_threshold=1000, autovacuum_analyze_scale_factor=0.02, autovacuum_analyze_threshold=1000, autovacuum_vacuum_cost_limit=2000)',
        partition_name
      );
    END IF;
    month_start := (month_start + INTERVAL '1 month')::date;
  END LOOP;
END;
$$ LANGUAGE plpgsql;

-- Cover every existing month plus twelve future months before copying data.
SELECT public.goflow_create_month_partitions(
  'historic_process_instances', 'historic_process_instances',
  COALESCE((SELECT date_trunc('month', MIN(ended_at))::date FROM public.historic_process_instances_pre015), date_trunc('month', CURRENT_DATE)::date),
  GREATEST(COALESCE((SELECT date_trunc('month', MAX(ended_at))::date FROM public.historic_process_instances_pre015), CURRENT_DATE), (CURRENT_DATE + INTERVAL '12 months')::date)
);
SELECT public.goflow_create_month_partitions(
  'historic_activity_instances', 'historic_activity_instances',
  COALESCE((SELECT date_trunc('month', MIN(occurred_at))::date FROM public.historic_activity_instances_pre015), date_trunc('month', CURRENT_DATE)::date),
  GREATEST(COALESCE((SELECT date_trunc('month', MAX(occurred_at))::date FROM public.historic_activity_instances_pre015), CURRENT_DATE), (CURRENT_DATE + INTERVAL '12 months')::date)
);
SELECT public.goflow_create_month_partitions(
  'historic_tasks', 'historic_tasks',
  COALESCE((SELECT date_trunc('month', MIN(created_at))::date FROM public.historic_tasks_pre015), date_trunc('month', CURRENT_DATE)::date),
  GREATEST(COALESCE((SELECT date_trunc('month', MAX(created_at))::date FROM public.historic_tasks_pre015), CURRENT_DATE), (CURRENT_DATE + INTERVAL '12 months')::date)
);
SELECT public.goflow_create_month_partitions(
  'historic_variables', 'historic_variables',
  COALESCE((SELECT date_trunc('month', MIN(updated_at))::date FROM public.historic_variables_pre015), date_trunc('month', CURRENT_DATE)::date),
  GREATEST(COALESCE((SELECT date_trunc('month', MAX(updated_at))::date FROM public.historic_variables_pre015), CURRENT_DATE), (CURRENT_DATE + INTERVAL '12 months')::date)
);
SELECT public.goflow_create_month_partitions(
  'historic_incidents', 'historic_incidents',
  COALESCE((SELECT date_trunc('month', MIN(created_at))::date FROM public.historic_incidents_pre015), date_trunc('month', CURRENT_DATE)::date),
  GREATEST(COALESCE((SELECT date_trunc('month', MAX(created_at))::date FROM public.historic_incidents_pre015), CURRENT_DATE), (CURRENT_DATE + INTERVAL '12 months')::date)
);
SELECT public.goflow_create_month_partitions(
  'audit_logs', 'audit_logs',
  COALESCE((SELECT date_trunc('month', MIN(created_at))::date FROM public.audit_logs_pre015), date_trunc('month', CURRENT_DATE)::date),
  GREATEST(COALESCE((SELECT date_trunc('month', MAX(created_at))::date FROM public.audit_logs_pre015), CURRENT_DATE), (CURRENT_DATE + INTERVAL '12 months')::date)
);

CREATE TABLE public.historic_process_instances_default
  PARTITION OF public.historic_process_instances DEFAULT;
CREATE TABLE public.historic_activity_instances_default
  PARTITION OF public.historic_activity_instances DEFAULT;
CREATE TABLE public.historic_tasks_default
  PARTITION OF public.historic_tasks DEFAULT;
CREATE TABLE public.historic_variables_default
  PARTITION OF public.historic_variables DEFAULT;
CREATE TABLE public.historic_incidents_default
  PARTITION OF public.historic_incidents DEFAULT;
CREATE TABLE public.audit_logs_default
  PARTITION OF public.audit_logs DEFAULT;
ALTER TABLE public.audit_logs_default SET (
  autovacuum_vacuum_scale_factor = 0.05,
  autovacuum_vacuum_threshold = 1000,
  autovacuum_analyze_scale_factor = 0.02,
  autovacuum_analyze_threshold = 1000,
  autovacuum_vacuum_cost_limit = 2000
);

INSERT INTO public.historic_process_instances SELECT * FROM public.historic_process_instances_pre015;
INSERT INTO public.historic_activity_instances SELECT * FROM public.historic_activity_instances_pre015;
INSERT INTO public.historic_tasks SELECT * FROM public.historic_tasks_pre015;
INSERT INTO public.historic_variables SELECT * FROM public.historic_variables_pre015;
INSERT INTO public.historic_incidents SELECT * FROM public.historic_incidents_pre015;
INSERT INTO public.audit_logs SELECT * FROM public.audit_logs_pre015;

DROP TABLE public.historic_activity_instances_pre015;
DROP TABLE public.historic_tasks_pre015;
DROP TABLE public.historic_variables_pre015;
DROP TABLE public.historic_incidents_pre015;
DROP TABLE public.historic_process_instances_pre015;
DROP TABLE public.audit_logs_pre015;

CREATE INDEX idx_historic_process_instances_definition_key ON public.historic_process_instances(process_definition_key);
CREATE INDEX idx_historic_process_instances_tenant_id ON public.historic_process_instances(tenant_id);
CREATE INDEX idx_historic_process_instances_business_key ON public.historic_process_instances(business_key);
CREATE INDEX idx_historic_process_instances_started_at ON public.historic_process_instances(started_at);
CREATE INDEX idx_historic_process_instances_ended_at ON public.historic_process_instances(ended_at);
CREATE INDEX idx_historic_process_instances_key_started_at ON public.historic_process_instances(process_definition_key, started_at);
CREATE INDEX idx_historic_process_instances_started_brin ON public.historic_process_instances USING BRIN(started_at);

CREATE INDEX idx_historic_activity_instances_process_instance_id ON public.historic_activity_instances(process_instance_id);
CREATE INDEX idx_historic_tasks_process_instance_id ON public.historic_tasks(process_instance_id);
CREATE INDEX idx_historic_variables_process_instance_id ON public.historic_variables(process_instance_id);
CREATE INDEX idx_historic_incidents_process_definition_key ON public.historic_incidents(process_definition_key);
CREATE INDEX idx_historic_incidents_process_instance_id ON public.historic_incidents(process_instance_id);
CREATE INDEX uq_historic_tasks_legacy_id ON public.historic_tasks(id) WHERE id IS NOT NULL;
CREATE INDEX uq_historic_incidents_legacy_id ON public.historic_incidents(id) WHERE id IS NOT NULL;
CREATE INDEX idx_audit_logs_instance_key ON public.audit_logs(process_instance_key);
CREATE INDEX idx_audit_logs_task_key ON public.audit_logs(task_key);
CREATE INDEX idx_audit_logs_created_brin ON public.audit_logs USING BRIN(created_at);

COMMENT ON TABLE public.audit_logs IS 'Monthly-partitioned audit trail';
COMMENT ON TABLE public.historic_process_instances IS 'Monthly history partitions by ended_at';

-- Called at migration time and at application startup. DEFAULT partitions
-- remain a safety net if the application runs for more than a year without a
-- restart; the next call creates another twelve months.
CREATE OR REPLACE FUNCTION public.goflow_ensure_history_partitions(months_ahead INTEGER DEFAULT 12)
RETURNS void AS $$
DECLARE
  first_month DATE := date_trunc('month', CURRENT_DATE)::date;
  last_month DATE := (first_month + make_interval(months => GREATEST(months_ahead, 1)))::date;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('goflow_history_partitions', 0));
  PERFORM public.goflow_create_month_partitions('historic_process_instances', 'historic_process_instances', first_month, last_month);
  PERFORM public.goflow_create_month_partitions('historic_activity_instances', 'historic_activity_instances', first_month, last_month);
  PERFORM public.goflow_create_month_partitions('historic_tasks', 'historic_tasks', first_month, last_month);
  PERFORM public.goflow_create_month_partitions('historic_variables', 'historic_variables', first_month, last_month);
  PERFORM public.goflow_create_month_partitions('historic_incidents', 'historic_incidents', first_month, last_month);
  PERFORM public.goflow_create_month_partitions('audit_logs', 'audit_logs', first_month, last_month);
END;
$$ LANGUAGE plpgsql;

SELECT public.goflow_ensure_history_partitions(12);

INSERT INTO public.schema_migrations (version, description)
VALUES ('015', 'partitionnement mensuel de l historique et des audits')
ON CONFLICT (version) DO NOTHING;

COMMIT;
