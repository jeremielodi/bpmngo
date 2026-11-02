-- 019_partition_retention.sql
-- Remove empty, fully expired monthly partitions after row-level retention.
--
-- Row-level retention remains authoritative because activity/audit timestamps
-- need not match the process end month. A partition is detached and dropped
-- only after retention emptied it and its upper bound is older than cutoff.

BEGIN;

CREATE OR REPLACE FUNCTION public.goflow_drop_empty_history_partitions(
  cutoff TIMESTAMP WITHOUT TIME ZONE
) RETURNS TABLE(partition_name TEXT) AS $$
DECLARE
  part RECORD;
  bounds TEXT[];
  upper_bound TIMESTAMP WITHOUT TIME ZONE;
  empty_partition BOOLEAN;
BEGIN
  FOR part IN
    SELECT parent.relname AS parent_name,
           child.relname AS child_name,
           pg_get_expr(child.relpartbound, child.oid) AS partition_bound
    FROM pg_inherits inheritance
    JOIN pg_class parent ON parent.oid = inheritance.inhparent
    JOIN pg_class child ON child.oid = inheritance.inhrelid
    JOIN pg_namespace namespace ON namespace.oid = parent.relnamespace
    WHERE namespace.nspname = 'public'
      AND parent.relname IN (
        'historic_process_instances', 'historic_activity_instances',
        'historic_tasks', 'historic_variables', 'historic_incidents',
        'audit_logs'
      )
      AND child.relname NOT LIKE '%\_default' ESCAPE '\'
    ORDER BY parent.relname, child.relname
  LOOP
    bounds := regexp_match(part.partition_bound, 'TO \(''([^'']+)''\)');
    IF bounds IS NULL THEN
      CONTINUE;
    END IF;
    upper_bound := bounds[1]::timestamp;
    IF upper_bound > cutoff THEN
      CONTINUE;
    END IF;

    -- Prevent a late archive insert between the emptiness check and detach.
    EXECUTE format('LOCK TABLE public.%I IN ACCESS EXCLUSIVE MODE', part.child_name);
    EXECUTE format(
      'SELECT NOT EXISTS (SELECT 1 FROM public.%I LIMIT 1)',
      part.child_name
    ) INTO empty_partition;
    IF NOT empty_partition THEN
      CONTINUE;
    END IF;

    EXECUTE format(
      'ALTER TABLE public.%I DETACH PARTITION public.%I',
      part.parent_name, part.child_name
    );
    EXECUTE format('DROP TABLE public.%I', part.child_name);
    partition_name := part.child_name;
    RETURN NEXT;
  END LOOP;
END;
$$ LANGUAGE plpgsql;

INSERT INTO public.schema_migrations (version, description)
VALUES ('019', 'purge sure des partitions historiques vides selon la retention')
ON CONFLICT (version) DO NOTHING;

COMMIT;
