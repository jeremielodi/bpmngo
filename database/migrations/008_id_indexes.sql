-- 008_id_indexes.sql — index sur les identifiants seuls.
--
-- Depuis Citus (phase 2), la clé primaire de executions et de incidents est
-- (process_instance_id, id) : une requête qui cherche une ligne par son seul
-- id ne peut pas s'en servir, et parcourait toute la table. C'est le cas,
-- à chaque job, de la lecture de l'élément courant des jobs verrouillés
-- (executions WHERE id IN (...)) et de la reprise d'une exécution
-- (executions WHERE id = ...), mesuré sous charge ; et pour incidents de la
-- lecture d'un incident par son id et des incidents d'un job.
--
-- Ces tables ne gardent que les instances en cours (les terminées partent
-- dans historic_*) : la création des index est rapide.
--
--   psql -h <hôte> -U <user> -d <base> -v ON_ERROR_STOP=1 -1 -f 008_id_indexes.sql

CREATE INDEX IF NOT EXISTS idx_executions_id ON public.executions (id);
CREATE INDEX IF NOT EXISTS idx_incidents_id ON public.incidents (id);
CREATE INDEX IF NOT EXISTS idx_incidents_job_id ON public.incidents (job_id);

INSERT INTO public.schema_migrations (version, description)
VALUES ('008', 'index sur executions.id, incidents.id et incidents.job_id')
ON CONFLICT (version) DO NOTHING;
