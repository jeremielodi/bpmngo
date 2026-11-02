-- 023_historic_instance_engine_key.sql
-- La clé entière d'une instance (celle de /v2 et de gRPC) disparaissait à
-- l'archivage : historic_process_instances n'en gardait pas trace. Une
-- instance terminée ne se retrouvait plus par sa clé (sauf dans le cache de
-- l'instance BpmnGo qui l'avait vue), et sa clé était même réattribuée,
-- différente, au premier affichage de son historique.
--
-- Les instances archivées avant cette migration gardent engine_key NULL :
-- leur clé éventuelle reste celle de zeebe_keys.

BEGIN;

ALTER TABLE public.historic_process_instances
  ADD COLUMN IF NOT EXISTS engine_key BIGINT;

CREATE INDEX IF NOT EXISTS idx_historic_process_instances_engine_key
  ON public.historic_process_instances (engine_key)
  WHERE engine_key IS NOT NULL;

INSERT INTO public.schema_migrations (version, description)
VALUES ('023', 'historic_process_instances.engine_key : clé entière conservée à l archivage')
ON CONFLICT (version) DO NOTHING;

COMMIT;
