CREATE TABLE IF NOT EXISTS public.simulation_scenarios (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    bpmn_xml TEXT NOT NULL,
    configuration JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    tenant_id TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

ALTER TABLE public.simulation_scenarios ADD COLUMN IF NOT EXISTS tenant_id TEXT;

CREATE INDEX IF NOT EXISTS idx_simulation_scenarios_created_at
    ON public.simulation_scenarios(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_simulation_scenarios_tenant
    ON public.simulation_scenarios(tenant_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS public.simulation_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scenario_id UUID NOT NULL REFERENCES public.simulation_scenarios(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    result JSONB,
    error_message TEXT,
    created_by UUID REFERENCES public.users(id) ON DELETE SET NULL,
    started_at TIMESTAMP NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_simulation_runs_scenario_started
    ON public.simulation_runs(scenario_id, started_at DESC);

CREATE TABLE IF NOT EXISTS public.simulation_issues (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scenario_id UUID NOT NULL REFERENCES public.simulation_scenarios(id) ON DELETE CASCADE,
    source_run_id UUID REFERENCES public.simulation_runs(id) ON DELETE SET NULL,
    code TEXT NOT NULL,
    element_id TEXT,
    title TEXT NOT NULL,
    evidence TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT 'other',
    severity TEXT NOT NULL DEFAULT 'medium' CHECK (severity IN ('low', 'medium', 'high')),
    suggestion TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'resolved', 'dismissed')),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_simulation_issues_scenario_status
    ON public.simulation_issues(scenario_id, status, created_at DESC);

INSERT INTO public.schema_migrations (version, description)
VALUES ('020', 'laboratoire de simulation BPMN, campagnes et registre de problèmes')
ON CONFLICT (version) DO NOTHING;
