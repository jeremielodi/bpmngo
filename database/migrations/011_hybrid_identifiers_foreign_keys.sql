-- 011_hybrid_identifiers_foreign_keys.sql
-- Add the BIGINT relationship graph while old binaries still write UUID
-- relationship columns. Triggers provide rolling-upgrade dual writes.

ALTER TABLE public.process_definitions ADD COLUMN IF NOT EXISTS deployment_key BIGINT;
ALTER TABLE public.process_instances
  ADD COLUMN IF NOT EXISTS process_definition_key BIGINT,
  ADD COLUMN IF NOT EXISTS parent_process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS parent_execution_key BIGINT;
ALTER TABLE public.executions
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS parent_execution_key BIGINT;
ALTER TABLE public.jobs
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS execution_key BIGINT;
ALTER TABLE public.tasks
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS execution_key BIGINT;
ALTER TABLE public.timer_jobs
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS execution_key BIGINT;
ALTER TABLE public.incidents
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT,
  ADD COLUMN IF NOT EXISTS job_key BIGINT,
  ADD COLUMN IF NOT EXISTS execution_key BIGINT;
ALTER TABLE public.variables ADD COLUMN IF NOT EXISTS process_instance_key BIGINT;
ALTER TABLE public.published_message_correlations
  ADD COLUMN IF NOT EXISTS published_message_key BIGINT,
  ADD COLUMN IF NOT EXISTS process_instance_key BIGINT;

UPDATE public.process_definitions d SET deployment_key = p.engine_key
FROM public.deployments p WHERE d.deployment_id = p.id AND d.deployment_key IS NULL;
UPDATE public.process_instances i SET process_definition_key = d.engine_key
FROM public.process_definitions d WHERE i.process_definition_id = d.id AND i.process_definition_key IS NULL;
UPDATE public.process_instances c SET parent_process_instance_key = p.engine_key
FROM public.process_instances p WHERE c.parent_instance_id = p.id AND c.parent_process_instance_key IS NULL;
UPDATE public.process_instances i SET parent_execution_key = e.engine_key
FROM public.executions e WHERE i.parent_execution_id = e.id AND i.parent_execution_key IS NULL;
UPDATE public.executions e SET process_instance_key = p.engine_key
FROM public.process_instances p WHERE e.process_instance_id = p.id AND e.process_instance_key IS NULL;
UPDATE public.executions c SET parent_execution_key = p.engine_key
FROM public.executions p WHERE c.parent_execution_id = p.id AND c.parent_execution_key IS NULL;
UPDATE public.jobs j SET process_instance_key = p.engine_key
FROM public.process_instances p WHERE j.process_instance_id = p.id AND j.process_instance_key IS NULL;
UPDATE public.jobs j SET execution_key = e.engine_key
FROM public.executions e WHERE j.execution_id = e.id AND j.execution_key IS NULL;
UPDATE public.tasks t SET process_instance_key = p.engine_key
FROM public.process_instances p WHERE t.process_instance_id = p.id AND t.process_instance_key IS NULL;
UPDATE public.tasks t SET execution_key = e.engine_key
FROM public.executions e WHERE t.execution_id = e.id AND t.execution_key IS NULL;
UPDATE public.timer_jobs t SET process_instance_key = p.engine_key
FROM public.process_instances p WHERE t.process_instance_id = p.id AND t.process_instance_key IS NULL;
UPDATE public.timer_jobs t SET execution_key = e.engine_key
FROM public.executions e WHERE t.execution_id = e.id AND t.execution_key IS NULL;
UPDATE public.incidents i SET process_instance_key = p.engine_key
FROM public.process_instances p WHERE i.process_instance_id = p.id AND i.process_instance_key IS NULL;
UPDATE public.incidents i SET job_key = j.engine_key
FROM public.jobs j WHERE i.job_id = j.id AND i.job_key IS NULL;
UPDATE public.incidents i SET execution_key = e.engine_key
FROM public.executions e WHERE i.execution_id = e.id AND i.execution_key IS NULL;
UPDATE public.variables v SET process_instance_key = p.engine_key
FROM public.process_instances p WHERE v.process_instance_id = p.id AND v.process_instance_key IS NULL;
UPDATE public.published_message_correlations c SET published_message_key = m.engine_key
FROM public.published_messages m WHERE c.published_message_id = m.id AND c.published_message_key IS NULL;
UPDATE public.published_message_correlations c SET process_instance_key = p.engine_key
FROM public.process_instances p WHERE c.process_instance_id = p.id AND c.process_instance_key IS NULL;

ALTER TABLE public.process_definitions ALTER COLUMN deployment_key SET NOT NULL;
ALTER TABLE public.process_instances ALTER COLUMN process_definition_key SET NOT NULL;
ALTER TABLE public.executions ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.jobs ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.tasks ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.timer_jobs ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.incidents ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.variables ALTER COLUMN process_instance_key SET NOT NULL;
ALTER TABLE public.published_message_correlations ALTER COLUMN published_message_key SET NOT NULL;
-- process_instance_key stays nullable for the short message-start reservation:
-- startOneByMessage claims (message, process) with uuid.Nil before creating the
-- instance, then updates process_instance_id in the same delivery flow. The
-- trigger fills this BIGINT key on that UPDATE.

ALTER TABLE public.process_definitions ADD CONSTRAINT process_definitions_deployment_key_fkey
  FOREIGN KEY (deployment_key) REFERENCES public.deployments(engine_key) ON DELETE CASCADE;
ALTER TABLE public.process_instances ADD CONSTRAINT process_instances_process_definition_key_fkey
  FOREIGN KEY (process_definition_key) REFERENCES public.process_definitions(engine_key);
ALTER TABLE public.process_instances ADD CONSTRAINT process_instances_parent_process_instance_key_fkey
  FOREIGN KEY (parent_process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE SET NULL;
ALTER TABLE public.process_instances ADD CONSTRAINT process_instances_parent_execution_key_fkey
  FOREIGN KEY (parent_execution_key) REFERENCES public.executions(engine_key) ON DELETE SET NULL;
ALTER TABLE public.executions ADD CONSTRAINT executions_process_instance_key_fkey
  FOREIGN KEY (process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE CASCADE;
ALTER TABLE public.executions ADD CONSTRAINT executions_parent_execution_key_fkey
  FOREIGN KEY (parent_execution_key) REFERENCES public.executions(engine_key) ON DELETE SET NULL;
ALTER TABLE public.jobs ADD CONSTRAINT jobs_process_instance_key_fkey
  FOREIGN KEY (process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE CASCADE;
ALTER TABLE public.jobs ADD CONSTRAINT jobs_execution_key_fkey
  FOREIGN KEY (execution_key) REFERENCES public.executions(engine_key) ON DELETE SET NULL;
ALTER TABLE public.tasks ADD CONSTRAINT tasks_process_instance_key_fkey
  FOREIGN KEY (process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE CASCADE;
ALTER TABLE public.tasks ADD CONSTRAINT tasks_execution_key_fkey
  FOREIGN KEY (execution_key) REFERENCES public.executions(engine_key) ON DELETE SET NULL;
ALTER TABLE public.timer_jobs ADD CONSTRAINT timer_jobs_process_instance_key_fkey
  FOREIGN KEY (process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE CASCADE;
ALTER TABLE public.timer_jobs ADD CONSTRAINT timer_jobs_execution_key_fkey
  FOREIGN KEY (execution_key) REFERENCES public.executions(engine_key) ON DELETE SET NULL;
ALTER TABLE public.incidents ADD CONSTRAINT incidents_process_instance_key_fkey
  FOREIGN KEY (process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE CASCADE;
ALTER TABLE public.incidents ADD CONSTRAINT incidents_job_key_fkey
  FOREIGN KEY (job_key) REFERENCES public.jobs(engine_key) ON DELETE SET NULL;
ALTER TABLE public.incidents ADD CONSTRAINT incidents_execution_key_fkey
  FOREIGN KEY (execution_key) REFERENCES public.executions(engine_key) ON DELETE SET NULL;
ALTER TABLE public.variables ADD CONSTRAINT variables_process_instance_key_fkey
  FOREIGN KEY (process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE CASCADE;
ALTER TABLE public.published_message_correlations ADD CONSTRAINT published_message_correlations_message_key_fkey
  FOREIGN KEY (published_message_key) REFERENCES public.published_messages(engine_key) ON DELETE CASCADE;
ALTER TABLE public.published_message_correlations ADD CONSTRAINT published_message_correlations_instance_key_fkey
  FOREIGN KEY (process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_process_definitions_deployment_key ON public.process_definitions(deployment_key);
CREATE INDEX IF NOT EXISTS idx_process_instances_definition_key ON public.process_instances(process_definition_key);
CREATE INDEX IF NOT EXISTS idx_executions_instance_key ON public.executions(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_jobs_instance_key ON public.jobs(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_jobs_execution_key ON public.jobs(execution_key);
CREATE INDEX IF NOT EXISTS idx_tasks_instance_key ON public.tasks(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_tasks_execution_key ON public.tasks(execution_key);
CREATE INDEX IF NOT EXISTS idx_timer_jobs_instance_key ON public.timer_jobs(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_timer_jobs_execution_key ON public.timer_jobs(execution_key);
CREATE INDEX IF NOT EXISTS idx_incidents_instance_key ON public.incidents(process_instance_key);
CREATE INDEX IF NOT EXISTS idx_variables_instance_key ON public.variables(process_instance_key);

CREATE OR REPLACE FUNCTION public.fill_hybrid_relationship_keys()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  CASE TG_TABLE_NAME
    WHEN 'process_definitions' THEN
      SELECT engine_key INTO NEW.deployment_key FROM public.deployments WHERE id = NEW.deployment_id;
    WHEN 'process_instances' THEN
      SELECT engine_key INTO NEW.process_definition_key FROM public.process_definitions WHERE id = NEW.process_definition_id;
      SELECT engine_key INTO NEW.parent_process_instance_key FROM public.process_instances WHERE id = NEW.parent_instance_id;
      SELECT engine_key INTO NEW.parent_execution_key FROM public.executions WHERE id = NEW.parent_execution_id;
    WHEN 'executions' THEN
      SELECT engine_key INTO NEW.process_instance_key FROM public.process_instances WHERE id = NEW.process_instance_id;
      SELECT engine_key INTO NEW.parent_execution_key FROM public.executions WHERE id = NEW.parent_execution_id;
    WHEN 'jobs' THEN
      SELECT engine_key INTO NEW.process_instance_key FROM public.process_instances WHERE id = NEW.process_instance_id;
      SELECT engine_key INTO NEW.execution_key FROM public.executions WHERE id = NEW.execution_id;
    WHEN 'tasks' THEN
      SELECT engine_key INTO NEW.process_instance_key FROM public.process_instances WHERE id = NEW.process_instance_id;
      SELECT engine_key INTO NEW.execution_key FROM public.executions WHERE id = NEW.execution_id;
    WHEN 'timer_jobs' THEN
      SELECT engine_key INTO NEW.process_instance_key FROM public.process_instances WHERE id = NEW.process_instance_id;
      SELECT engine_key INTO NEW.execution_key FROM public.executions WHERE id = NEW.execution_id;
    WHEN 'incidents' THEN
      SELECT engine_key INTO NEW.process_instance_key FROM public.process_instances WHERE id = NEW.process_instance_id;
      SELECT engine_key INTO NEW.job_key FROM public.jobs WHERE id = NEW.job_id;
      SELECT engine_key INTO NEW.execution_key FROM public.executions WHERE id = NEW.execution_id;
    WHEN 'variables' THEN
      SELECT engine_key INTO NEW.process_instance_key FROM public.process_instances WHERE id = NEW.process_instance_id;
    WHEN 'published_message_correlations' THEN
      SELECT engine_key INTO NEW.published_message_key FROM public.published_messages WHERE id = NEW.published_message_id;
      SELECT engine_key INTO NEW.process_instance_key FROM public.process_instances WHERE id = NEW.process_instance_id;
  END CASE;
  RETURN NEW;
END;
$$;

CREATE TRIGGER trg_process_definitions_hybrid_relationships BEFORE INSERT OR UPDATE OF deployment_id ON public.process_definitions FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();
CREATE TRIGGER trg_process_instances_hybrid_relationships BEFORE INSERT OR UPDATE OF process_definition_id, parent_instance_id, parent_execution_id ON public.process_instances FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();
CREATE TRIGGER trg_executions_hybrid_relationships BEFORE INSERT OR UPDATE OF process_instance_id, parent_execution_id ON public.executions FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();
CREATE TRIGGER trg_jobs_hybrid_relationships BEFORE INSERT OR UPDATE OF process_instance_id, execution_id ON public.jobs FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();
CREATE TRIGGER trg_tasks_hybrid_relationships BEFORE INSERT OR UPDATE OF process_instance_id, execution_id ON public.tasks FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();
CREATE TRIGGER trg_timer_jobs_hybrid_relationships BEFORE INSERT OR UPDATE OF process_instance_id, execution_id ON public.timer_jobs FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();
CREATE TRIGGER trg_incidents_hybrid_relationships BEFORE INSERT OR UPDATE OF process_instance_id, job_id, execution_id ON public.incidents FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();
CREATE TRIGGER trg_variables_hybrid_relationships BEFORE INSERT OR UPDATE OF process_instance_id ON public.variables FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();
CREATE TRIGGER trg_published_message_correlations_hybrid_relationships BEFORE INSERT OR UPDATE OF published_message_id, process_instance_id ON public.published_message_correlations FOR EACH ROW EXECUTE FUNCTION public.fill_hybrid_relationship_keys();

INSERT INTO public.schema_migrations (version, description)
VALUES ('011', 'clés étrangères BIGINT et double écriture UUID/BIGINT')
ON CONFLICT (version) DO NOTHING;
