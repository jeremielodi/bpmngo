-- 010_hybrid_identifiers_primary_keys.sql
-- Promote the phase-009 engine keys to the physical primary keys.
--
-- The old UUID `id` columns remain UNIQUE compatibility shadows during the
-- application rolling migration. They are no longer primary keys. Aggregate
-- roots keep their durable UUID identity in `uid`; hot entities lose their
-- UUID shadows in the final cleanup migration once no old binary is running.

-- UUID relationships must have a replacement unique index before their old
-- referenced PK constraint can be removed.
CREATE UNIQUE INDEX IF NOT EXISTS uq_deployments_legacy_id ON public.deployments(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_process_definitions_legacy_id ON public.process_definitions(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_process_instances_legacy_id ON public.process_instances(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_published_messages_legacy_id ON public.published_messages(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_executions_legacy_id ON public.executions(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_jobs_legacy_id ON public.jobs(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tasks_legacy_id ON public.tasks(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_timer_jobs_legacy_id ON public.timer_jobs(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_incidents_legacy_id ON public.incidents(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_variables_legacy_id ON public.variables(id);

-- Drop only FKs whose dependency pins one of the UUID primary-key
-- constraints. They are recreated unchanged against the UUID unique index,
-- allowing old and new application versions to overlap safely.
ALTER TABLE public.process_definitions DROP CONSTRAINT process_definitions_deployment_id_fkey;
ALTER TABLE public.process_instances DROP CONSTRAINT process_instances_process_definition_id_fkey;
ALTER TABLE public.gateway_state DROP CONSTRAINT gateway_state_process_instance_id_fkey;
ALTER TABLE public.executions DROP CONSTRAINT executions_process_instance_id_fkey;
ALTER TABLE public.tasks DROP CONSTRAINT tasks_process_instance_id_fkey;
ALTER TABLE public.variables DROP CONSTRAINT variables_process_instance_id_fkey;
ALTER TABLE public.incidents DROP CONSTRAINT incidents_process_instance_id_fkey;
ALTER TABLE public.multi_instance_executions DROP CONSTRAINT multi_instance_executions_process_instance_id_fkey;
ALTER TABLE public.dmn_decisions DROP CONSTRAINT dmn_decisions_deployment_id_fkey;
ALTER TABLE public.forms DROP CONSTRAINT forms_deployment_id_fkey;
ALTER TABLE public.published_message_correlations DROP CONSTRAINT published_message_correlations_published_message_id_fkey;

ALTER TABLE public.deployments DROP CONSTRAINT deployments_pkey;
ALTER TABLE public.process_definitions DROP CONSTRAINT process_definitions_pkey;
ALTER TABLE public.process_instances DROP CONSTRAINT process_instances_pkey;
ALTER TABLE public.published_messages DROP CONSTRAINT published_messages_pkey;
ALTER TABLE public.executions DROP CONSTRAINT executions_pkey;
ALTER TABLE public.jobs DROP CONSTRAINT jobs_pkey;
ALTER TABLE public.tasks DROP CONSTRAINT tasks_pkey;
ALTER TABLE public.timer_jobs DROP CONSTRAINT timer_jobs_pkey;
ALTER TABLE public.incidents DROP CONSTRAINT incidents_pkey;
ALTER TABLE public.variables DROP CONSTRAINT variables_pkey;

ALTER TABLE public.deployments ADD CONSTRAINT deployments_pkey PRIMARY KEY USING INDEX uq_deployments_engine_key;
ALTER TABLE public.process_definitions ADD CONSTRAINT process_definitions_pkey PRIMARY KEY USING INDEX uq_process_definitions_engine_key;
ALTER TABLE public.process_instances ADD CONSTRAINT process_instances_pkey PRIMARY KEY USING INDEX uq_process_instances_engine_key;
ALTER TABLE public.published_messages ADD CONSTRAINT published_messages_pkey PRIMARY KEY USING INDEX uq_published_messages_engine_key;
ALTER TABLE public.executions ADD CONSTRAINT executions_pkey PRIMARY KEY USING INDEX uq_executions_engine_key;
ALTER TABLE public.jobs ADD CONSTRAINT jobs_pkey PRIMARY KEY USING INDEX uq_jobs_engine_key;
ALTER TABLE public.tasks ADD CONSTRAINT tasks_pkey PRIMARY KEY USING INDEX uq_tasks_engine_key;
ALTER TABLE public.timer_jobs ADD CONSTRAINT timer_jobs_pkey PRIMARY KEY USING INDEX uq_timer_jobs_engine_key;
ALTER TABLE public.incidents ADD CONSTRAINT incidents_pkey PRIMARY KEY USING INDEX uq_incidents_engine_key;
ALTER TABLE public.variables ADD CONSTRAINT variables_pkey PRIMARY KEY USING INDEX uq_variables_engine_key;

ALTER TABLE public.process_definitions ADD CONSTRAINT process_definitions_deployment_id_fkey
  FOREIGN KEY (deployment_id) REFERENCES public.deployments(id) ON DELETE CASCADE;
ALTER TABLE public.process_instances ADD CONSTRAINT process_instances_process_definition_id_fkey
  FOREIGN KEY (process_definition_id) REFERENCES public.process_definitions(id);
ALTER TABLE public.gateway_state ADD CONSTRAINT gateway_state_process_instance_id_fkey
  FOREIGN KEY (process_instance_id) REFERENCES public.process_instances(id);
ALTER TABLE public.executions ADD CONSTRAINT executions_process_instance_id_fkey
  FOREIGN KEY (process_instance_id) REFERENCES public.process_instances(id) ON DELETE CASCADE;
ALTER TABLE public.tasks ADD CONSTRAINT tasks_process_instance_id_fkey
  FOREIGN KEY (process_instance_id) REFERENCES public.process_instances(id) ON DELETE CASCADE;
ALTER TABLE public.variables ADD CONSTRAINT variables_process_instance_id_fkey
  FOREIGN KEY (process_instance_id) REFERENCES public.process_instances(id) ON DELETE CASCADE;
ALTER TABLE public.incidents ADD CONSTRAINT incidents_process_instance_id_fkey
  FOREIGN KEY (process_instance_id) REFERENCES public.process_instances(id) ON DELETE CASCADE;
ALTER TABLE public.multi_instance_executions ADD CONSTRAINT multi_instance_executions_process_instance_id_fkey
  FOREIGN KEY (process_instance_id) REFERENCES public.process_instances(id) ON DELETE CASCADE;
ALTER TABLE public.dmn_decisions ADD CONSTRAINT dmn_decisions_deployment_id_fkey
  FOREIGN KEY (deployment_id) REFERENCES public.deployments(id) ON DELETE CASCADE;
ALTER TABLE public.forms ADD CONSTRAINT forms_deployment_id_fkey
  FOREIGN KEY (deployment_id) REFERENCES public.deployments(id) ON DELETE CASCADE;
ALTER TABLE public.published_message_correlations ADD CONSTRAINT published_message_correlations_published_message_id_fkey
  FOREIGN KEY (published_message_id) REFERENCES public.published_messages(id) ON DELETE CASCADE;

INSERT INTO public.schema_migrations (version, description)
VALUES ('010', 'promotion des engine_key BIGINT en clés primaires')
ON CONFLICT (version) DO NOTHING;
