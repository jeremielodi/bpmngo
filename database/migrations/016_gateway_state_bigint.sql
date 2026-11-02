-- 016_gateway_state_bigint.sql
-- gateway_state is part of the per-instance hot colocation group. Move its
-- last UUID relationship to the same BIGINT distribution key as executions.

BEGIN;

ALTER TABLE public.gateway_state ADD COLUMN IF NOT EXISTS process_instance_key BIGINT;
UPDATE public.gateway_state g
SET process_instance_key = p.engine_key
FROM public.process_instances p
WHERE g.process_instance_id = p.id AND g.process_instance_key IS NULL;
ALTER TABLE public.gateway_state ALTER COLUMN process_instance_key SET NOT NULL;

ALTER TABLE public.gateway_state DROP CONSTRAINT IF EXISTS gateway_state_process_instance_id_fkey;
ALTER TABLE public.gateway_state DROP CONSTRAINT IF EXISTS gateway_state_process_instance_id_gateway_id_key;
ALTER TABLE public.gateway_state DROP CONSTRAINT IF EXISTS gateway_state_pkey;
ALTER TABLE public.gateway_state
  ADD CONSTRAINT gateway_state_pkey PRIMARY KEY (process_instance_key, id),
  ADD CONSTRAINT gateway_state_process_instance_key_gateway_id_key UNIQUE (process_instance_key, gateway_id),
  ADD CONSTRAINT gateway_state_process_instance_key_fkey
    FOREIGN KEY (process_instance_key) REFERENCES public.process_instances(engine_key) ON DELETE CASCADE;
ALTER TABLE public.gateway_state DROP COLUMN IF EXISTS process_instance_id;

INSERT INTO public.schema_migrations (version, description)
VALUES ('016', 'gateway_state distribué par process_instance_key BIGINT')
ON CONFLICT (version) DO NOTHING;

COMMIT;
