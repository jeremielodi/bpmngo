-- 020_hot_path_cpu.sql
-- Moins de CPU PostgreSQL par instance sur le chemin chaud
-- (start → job → complete → archivage).
--
-- 1. Index qui empêchaient les HOT updates. Une ligne d'exécution est mise
--    à jour ~6 fois par instance (status, current_element_id, is_active) :
--    tant qu'une de ces colonnes est indexée, chaque update réécrit une
--    entrée dans chacun des 5 index et laisse des versions mortes que les
--    lectures par instance parcourent (33 % de HOT seulement sous charge).
--    Aucune requête ne filtre executions sur ces colonnes sans la clé
--    d'instance (idx_scan = 0 et 3 après les 47 scénarios du benchmark).
--    De même, l'index GIN sur variables.data n'est utilisable par aucune
--    requête (elles comparent data->>'x', que jsonb_ops ne sert pas) mais
--    était réécrit à chaque écriture de variables.
-- 2. L'archiver et le contrôle d'admission lisent les instances terminées
--    par ended_at : index partiel au lieu de trier tout le backlog.
-- 3. process_instances avait deux FK vers la même ligne de
--    process_definitions (par id et par engine_key). Chaque démarrage
--    posait donc deux FOR KEY SHARE sur la ligne de la définition, partagée
--    par toutes les transactions concurrentes (MultiXact). La FK par
--    engine_key suffit : elle empêche toujours de supprimer une définition
--    encore utilisée.
-- 4. Le NOTIFY des variables ne concerne que les instances qui attendent
--    une condition : les autres n'en envoient plus (chaque NOTIFY prend un
--    verrou global au commit et réveillait le watcher pour rien). Le
--    watcher garde son balayage de sécurité toutes les 10 s.

BEGIN;

DROP INDEX IF EXISTS public.idx_executions_is_active;
DROP INDEX IF EXISTS public.idx_executions_current_element_id;
DROP INDEX IF EXISTS public.idx_variables_data;

CREATE INDEX IF NOT EXISTS idx_process_instances_finished_ended_at
  ON public.process_instances (ended_at)
  WHERE status IN ('completed', 'terminated');

ALTER TABLE public.process_instances
  DROP CONSTRAINT IF EXISTS process_instances_process_definition_id_fkey;

CREATE INDEX IF NOT EXISTS idx_event_subscriptions_conditional_open
  ON public.event_subscriptions (process_instance_key)
  WHERE event_type = 'conditional' AND consumed_at IS NULL;

CREATE OR REPLACE FUNCTION public.goflow_notify_variables() RETURNS trigger AS $$
DECLARE instance_uid UUID;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM public.event_subscriptions
    WHERE process_instance_key = NEW.process_instance_key
      AND event_type = 'conditional' AND consumed_at IS NULL
  ) THEN
    RETURN NULL;
  END IF;
  SELECT id INTO instance_uid FROM public.process_instances
  WHERE engine_key = NEW.process_instance_key;
  PERFORM pg_notify('goflow_variables', instance_uid::text);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

INSERT INTO public.schema_migrations (version, description)
VALUES ('020', 'chemin chaud : HOT updates, index archiver, FK et NOTIFY réduits')
ON CONFLICT (version) DO NOTHING;

COMMIT;
