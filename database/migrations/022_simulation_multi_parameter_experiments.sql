ALTER TABLE public.simulation_runs
    DROP CONSTRAINT IF EXISTS simulation_runs_type_check;

ALTER TABLE public.simulation_runs
    ADD CONSTRAINT simulation_runs_type_check
    CHECK (run_type IN ('simulation', 'capacity_sweep', 'experiment'));

INSERT INTO public.schema_migrations (version, description)
VALUES ('022', 'expériences de simulation multi-paramètres')
ON CONFLICT (version) DO NOTHING;
