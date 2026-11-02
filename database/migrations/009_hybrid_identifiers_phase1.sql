-- 009_hybrid_identifiers_phase1.sql
-- Additive first phase of the UUIDv7 + BIGINT migration.
--
-- Existing UUID ids remain authoritative until repositories have completed
-- dual-write/backfill verification. Existing public Zeebe keys are reused so
-- no client-visible key is renumbered. Unmapped legacy rows receive low-range
-- positive keys; new application-generated keys use the disjoint time-based
-- high range from internal/ids.

CREATE SEQUENCE IF NOT EXISTS public.engine_keys_legacy_seq AS BIGINT START 1;

SELECT setval(
  'public.engine_keys_legacy_seq',
  GREATEST(COALESCE((SELECT MAX(key) FROM public.zeebe_keys), 0) + 1, 1),
  false
);

-- During the rolling deployment the old application version does not yet
-- mention the additive columns. Defaults keep those writes valid. The
-- application-generated (Snowflake-style) keys occupy a much higher range,
-- so they cannot collide with this compatibility sequence.
CREATE OR REPLACE FUNCTION public.fill_hybrid_root_identity()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.engine_key IS NULL THEN
    NEW.engine_key := nextval('public.engine_keys_legacy_seq');
  END IF;
  IF NEW.uid IS NULL THEN
    NEW.uid := NEW.id;
  END IF;
  RETURN NEW;
END;
$$;

-- Aggregate roots retain a globally portable UUID identity and gain their
-- compact operational key.
ALTER TABLE public.deployments
  ADD COLUMN IF NOT EXISTS engine_key BIGINT,
  ADD COLUMN IF NOT EXISTS uid UUID;
UPDATE public.deployments d
SET uid = COALESCE(uid, id),
    engine_key = COALESCE(engine_key,
      (SELECT z.key FROM public.zeebe_keys z WHERE z.resource_type = 'deployment' AND z.resource_id = d.id),
      nextval('public.engine_keys_legacy_seq'))
WHERE uid IS NULL OR engine_key IS NULL;

ALTER TABLE public.process_definitions
  ADD COLUMN IF NOT EXISTS engine_key BIGINT,
  ADD COLUMN IF NOT EXISTS uid UUID;
UPDATE public.process_definitions d
SET uid = COALESCE(uid, id),
    engine_key = COALESCE(engine_key,
      (SELECT z.key FROM public.zeebe_keys z WHERE z.resource_type = 'process_definition' AND z.resource_id = d.id),
      nextval('public.engine_keys_legacy_seq'))
WHERE uid IS NULL OR engine_key IS NULL;

ALTER TABLE public.process_instances
  ADD COLUMN IF NOT EXISTS engine_key BIGINT,
  ADD COLUMN IF NOT EXISTS uid UUID;
UPDATE public.process_instances i
SET uid = COALESCE(uid, id),
    engine_key = COALESCE(engine_key,
      (SELECT z.key FROM public.zeebe_keys z WHERE z.resource_type = 'process_instance' AND z.resource_id = i.id),
      nextval('public.engine_keys_legacy_seq'))
WHERE uid IS NULL OR engine_key IS NULL;

ALTER TABLE public.published_messages
  ADD COLUMN IF NOT EXISTS engine_key BIGINT,
  ADD COLUMN IF NOT EXISTS uid UUID;
UPDATE public.published_messages m
SET uid = COALESCE(uid, id),
    engine_key = COALESCE(engine_key,
      (SELECT z.key FROM public.zeebe_keys z WHERE z.resource_type = 'message' AND z.resource_id = m.id),
      nextval('public.engine_keys_legacy_seq'))
WHERE uid IS NULL OR engine_key IS NULL;

-- Hot engine entities gain only an operational BIGINT identity.
ALTER TABLE public.executions ADD COLUMN IF NOT EXISTS engine_key BIGINT;
UPDATE public.executions e SET engine_key = COALESCE(engine_key,
  (SELECT z.key FROM public.zeebe_keys z WHERE z.resource_type = 'element_instance' AND z.resource_id = e.id),
  nextval('public.engine_keys_legacy_seq')) WHERE engine_key IS NULL;

ALTER TABLE public.jobs ADD COLUMN IF NOT EXISTS engine_key BIGINT;
UPDATE public.jobs j SET engine_key = COALESCE(engine_key,
  (SELECT z.key FROM public.zeebe_keys z WHERE z.resource_type = 'job' AND z.resource_id = j.id),
  nextval('public.engine_keys_legacy_seq')) WHERE engine_key IS NULL;

ALTER TABLE public.tasks ADD COLUMN IF NOT EXISTS engine_key BIGINT;
UPDATE public.tasks t SET engine_key = COALESCE(engine_key,
  (SELECT z.key FROM public.zeebe_keys z WHERE z.resource_type = 'user_task' AND z.resource_id = t.id),
  nextval('public.engine_keys_legacy_seq')) WHERE engine_key IS NULL;

ALTER TABLE public.timer_jobs ADD COLUMN IF NOT EXISTS engine_key BIGINT;
UPDATE public.timer_jobs SET engine_key = nextval('public.engine_keys_legacy_seq') WHERE engine_key IS NULL;

ALTER TABLE public.incidents ADD COLUMN IF NOT EXISTS engine_key BIGINT;
UPDATE public.incidents i SET engine_key = COALESCE(engine_key,
  (SELECT z.key FROM public.zeebe_keys z WHERE z.resource_type = 'incident' AND z.resource_id = i.id),
  nextval('public.engine_keys_legacy_seq')) WHERE engine_key IS NULL;

ALTER TABLE public.variables ADD COLUMN IF NOT EXISTS engine_key BIGINT;
UPDATE public.variables SET engine_key = nextval('public.engine_keys_legacy_seq') WHERE engine_key IS NULL;

-- Enforce the future identities without changing current PK/FK constraints.
ALTER TABLE public.deployments ALTER COLUMN engine_key SET NOT NULL, ALTER COLUMN uid SET NOT NULL;
ALTER TABLE public.process_definitions ALTER COLUMN engine_key SET NOT NULL, ALTER COLUMN uid SET NOT NULL;
ALTER TABLE public.process_instances ALTER COLUMN engine_key SET NOT NULL, ALTER COLUMN uid SET NOT NULL;
ALTER TABLE public.published_messages ALTER COLUMN engine_key SET NOT NULL, ALTER COLUMN uid SET NOT NULL;
ALTER TABLE public.executions ALTER COLUMN engine_key SET NOT NULL;
ALTER TABLE public.jobs ALTER COLUMN engine_key SET NOT NULL;
ALTER TABLE public.tasks ALTER COLUMN engine_key SET NOT NULL;
ALTER TABLE public.timer_jobs ALTER COLUMN engine_key SET NOT NULL;
ALTER TABLE public.incidents ALTER COLUMN engine_key SET NOT NULL;
ALTER TABLE public.variables ALTER COLUMN engine_key SET NOT NULL;

ALTER TABLE public.deployments ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.process_definitions ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.process_instances ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.published_messages ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.executions ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.jobs ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.tasks ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.timer_jobs ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.incidents ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');
ALTER TABLE public.variables ALTER COLUMN engine_key SET DEFAULT nextval('public.engine_keys_legacy_seq');

DROP TRIGGER IF EXISTS trg_deployments_hybrid_identity ON public.deployments;
CREATE TRIGGER trg_deployments_hybrid_identity BEFORE INSERT ON public.deployments
FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_root_identity();
DROP TRIGGER IF EXISTS trg_process_definitions_hybrid_identity ON public.process_definitions;
CREATE TRIGGER trg_process_definitions_hybrid_identity BEFORE INSERT ON public.process_definitions
FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_root_identity();
DROP TRIGGER IF EXISTS trg_process_instances_hybrid_identity ON public.process_instances;
CREATE TRIGGER trg_process_instances_hybrid_identity BEFORE INSERT ON public.process_instances
FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_root_identity();
DROP TRIGGER IF EXISTS trg_published_messages_hybrid_identity ON public.published_messages;
CREATE TRIGGER trg_published_messages_hybrid_identity BEFORE INSERT ON public.published_messages
FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_root_identity();

CREATE UNIQUE INDEX IF NOT EXISTS uq_deployments_engine_key ON public.deployments(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_deployments_uid ON public.deployments(uid);
CREATE UNIQUE INDEX IF NOT EXISTS uq_process_definitions_engine_key ON public.process_definitions(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_process_definitions_uid ON public.process_definitions(uid);
CREATE UNIQUE INDEX IF NOT EXISTS uq_process_instances_engine_key ON public.process_instances(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_process_instances_uid ON public.process_instances(uid);
CREATE UNIQUE INDEX IF NOT EXISTS uq_published_messages_engine_key ON public.published_messages(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_published_messages_uid ON public.published_messages(uid);
CREATE UNIQUE INDEX IF NOT EXISTS uq_executions_engine_key ON public.executions(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_jobs_engine_key ON public.jobs(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tasks_engine_key ON public.tasks(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_timer_jobs_engine_key ON public.timer_jobs(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_incidents_engine_key ON public.incidents(engine_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_variables_engine_key ON public.variables(engine_key);

INSERT INTO public.schema_migrations (version, description)
VALUES ('009', 'phase 1 identifiants hybrides BIGINT et UUIDv7')
ON CONFLICT (version) DO NOTHING;
