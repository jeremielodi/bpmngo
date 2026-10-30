-- 018_parent_fk_indexes.sql
-- PostgreSQL must find referencing rows for ON DELETE SET NULL/CASCADE.
-- Without these indexes, archiving one process instance scans the growing
-- executions/process_instances tables several times and becomes the dominant
-- CPU consumer under load.

BEGIN;

CREATE INDEX IF NOT EXISTS idx_process_instances_parent_instance_key
  ON public.process_instances(parent_process_instance_key)
  WHERE parent_process_instance_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_process_instances_parent_execution_key
  ON public.process_instances(parent_execution_key)
  WHERE parent_execution_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_executions_parent_execution_key
  ON public.executions(parent_execution_key)
  WHERE parent_execution_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_published_message_correlations_instance_key
  ON public.published_message_correlations(process_instance_key)
  WHERE process_instance_key IS NOT NULL;

INSERT INTO public.schema_migrations (version, description)
VALUES ('018', 'index des FK parentes pour supprimer les scans d archivage')
ON CONFLICT (version) DO NOTHING;

COMMIT;
