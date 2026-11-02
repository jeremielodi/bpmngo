-- 021_eventlog_projected_jobs_bigint.sql
-- Les jobs et les exécutions sont identifiés par des BIGINT depuis 013 : le
-- journal d'événements publie donc des identifiants entiers, que le
-- projecteur (mode shadow) ne savait plus écrire dans les colonnes UUID de
-- eventlog_projected_jobs — chaque événement de job était ignoré.
--
-- Cette table est un modèle de lecture dérivé du journal, sans autre
-- source : ses lignes (aux identifiants UUID d'avant 013) sont vidées, les
-- prochains événements la remplissent.

BEGIN;

TRUNCATE public.eventlog_projected_jobs;
ALTER TABLE public.eventlog_projected_jobs
  ALTER COLUMN id TYPE BIGINT USING NULL,
  ALTER COLUMN execution_id TYPE BIGINT USING NULL;

INSERT INTO public.schema_migrations (version, description)
VALUES ('021', 'eventlog_projected_jobs : identifiants de job et d exécution en BIGINT')
ON CONFLICT (version) DO NOTHING;

COMMIT;
