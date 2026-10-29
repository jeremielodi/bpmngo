ALTER TABLE public.simulation_scenarios
    ADD COLUMN IF NOT EXISTS current_version INTEGER NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS public.simulation_scenario_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scenario_id UUID NOT NULL REFERENCES public.simulation_scenarios(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    name TEXT NOT NULL,
    bpmn_xml TEXT NOT NULL,
    configuration JSONB NOT NULL,
    created_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (scenario_id, version)
);

INSERT INTO public.simulation_scenario_versions(scenario_id,version,name,bpmn_xml,configuration,created_by,created_at)
SELECT id,current_version,name,bpmn_xml,configuration,created_by,updated_at
FROM public.simulation_scenarios
ON CONFLICT (scenario_id,version) DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_simulation_scenario_versions_scenario
    ON public.simulation_scenario_versions(scenario_id, version DESC);

INSERT INTO public.schema_migrations (version, description)
VALUES ('023', 'versionnement, restauration et export des scénarios de simulation')
ON CONFLICT (version) DO NOTHING;
