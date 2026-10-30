-- Finish the hybrid-key rollout. Aggregate roots keep one public UUIDv7
-- (`id`) beside their BIGINT primary key; duplicate UUID shadows and the
-- rolling-upgrade triggers are no longer needed.

BEGIN;

-- Citus rejects ALTER TRIGGER on an already converted reference/distributed
-- relation, even with IF EXISTS. Only issue the DDL when the trigger is real;
-- Citus bootstrap already removed it before conversion.
DO $$
DECLARE
  item RECORD;
BEGIN
  FOR item IN
    SELECT * FROM (VALUES
      ('deployments', 'trg_deployments_hybrid_identity'),
      ('process_definitions', 'trg_process_definitions_hybrid_identity'),
      ('process_instances', 'trg_process_instances_hybrid_identity'),
      ('published_messages', 'trg_published_messages_hybrid_identity')
    ) AS v(table_name, trigger_name)
  LOOP
    IF EXISTS (
      SELECT 1 FROM pg_trigger
      WHERE tgrelid = ('public.' || item.table_name)::regclass
        AND tgname = item.trigger_name
    ) THEN
      EXECUTE format('DROP TRIGGER %I ON public.%I', item.trigger_name, item.table_name);
    END IF;
  END LOOP;
END;
$$;
DROP FUNCTION IF EXISTS public.fill_hybrid_root_identity();

ALTER TABLE public.deployments DROP COLUMN IF EXISTS uid;
ALTER TABLE public.process_definitions DROP COLUMN IF EXISTS uid;
ALTER TABLE public.process_instances DROP COLUMN IF EXISTS uid;
ALTER TABLE public.published_messages DROP COLUMN IF EXISTS uid;
ALTER TABLE public.executions DROP COLUMN IF EXISTS gateway_join_id;

INSERT INTO public.schema_migrations (version, description)
VALUES ('017', 'suppression des ombres UUID et triggers de compatibilité')
ON CONFLICT (version) DO NOTHING;

COMMIT;
