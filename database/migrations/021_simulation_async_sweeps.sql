ALTER TABLE public.simulation_runs
    DROP CONSTRAINT IF EXISTS simulation_runs_status_check;

ALTER TABLE public.simulation_runs
    ADD CONSTRAINT simulation_runs_status_check
    CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelled'));

ALTER TABLE public.simulation_runs
    ADD COLUMN IF NOT EXISTS run_type TEXT NOT NULL DEFAULT 'simulation',
    ADD COLUMN IF NOT EXISTS request JSONB,
    ADD COLUMN IF NOT EXISTS progress INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS worker_id TEXT,
    ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;

ALTER TABLE public.simulation_runs
    DROP CONSTRAINT IF EXISTS simulation_runs_type_check;
ALTER TABLE public.simulation_runs
    ADD CONSTRAINT simulation_runs_type_check
    CHECK (run_type IN ('simulation', 'capacity_sweep'));
ALTER TABLE public.simulation_runs
    DROP CONSTRAINT IF EXISTS simulation_runs_progress_check;
ALTER TABLE public.simulation_runs
    ADD CONSTRAINT simulation_runs_progress_check
    CHECK (progress BETWEEN 0 AND 100);

CREATE INDEX IF NOT EXISTS idx_simulation_runs_queue
    ON public.simulation_runs(status, started_at)
    WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS idx_simulation_runs_lease
    ON public.simulation_runs(heartbeat_at)
    WHERE status = 'running';

INSERT INTO public.schema_migrations (version, description)
VALUES ('021', 'campagnes de capacité et exécution asynchrone des simulations')
ON CONFLICT (version) DO NOTHING;
