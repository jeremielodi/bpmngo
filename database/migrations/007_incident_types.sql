-- 007_incident_types.sql — types d'incident de Camunda 8.
--
-- Les incidents sont désormais enregistrés avec le type de Camunda 8
-- (JOB_NO_RETRIES, CONDITION_ERROR, EXTRACT_VALUE_ERROR…), celui que
-- montrent l'API /v2 et le gRPC. L'API /engine-rest continue d'afficher les
-- anciens noms (failedExternalTask, executionError, unhandledErrorEvent).
--
-- Cette migration convertit les incidents déjà enregistrés, live et
-- archivés :
--   failedExternalTask avec un code d'erreur -> UNHANDLED_ERROR_EVENT
--   failedExternalTask sans code d'erreur    -> JOB_NO_RETRIES
--   unhandledErrorEvent                      -> UNHANDLED_ERROR_EVENT
--   executionError                           -> UNKNOWN (la cause précise
--                                               n'était pas enregistrée)
-- Idempotente : une seconde exécution ne trouve plus rien à convertir.
--
--   psql -h <hôte> -U <user> -d <base> -v ON_ERROR_STOP=1 -1 -f 007_incident_types.sql

UPDATE public.incidents SET incident_type = CASE
    WHEN incident_type = 'failedExternalTask' AND COALESCE(error_code, '') <> '' THEN 'UNHANDLED_ERROR_EVENT'
    WHEN incident_type = 'failedExternalTask' THEN 'JOB_NO_RETRIES'
    WHEN incident_type = 'unhandledErrorEvent' THEN 'UNHANDLED_ERROR_EVENT'
    ELSE 'UNKNOWN'
END
WHERE incident_type IN ('failedExternalTask', 'unhandledErrorEvent', 'executionError');

UPDATE public.historic_incidents SET incident_type = CASE
    WHEN incident_type = 'failedExternalTask' AND COALESCE(error_code, '') <> '' THEN 'UNHANDLED_ERROR_EVENT'
    WHEN incident_type = 'failedExternalTask' THEN 'JOB_NO_RETRIES'
    WHEN incident_type = 'unhandledErrorEvent' THEN 'UNHANDLED_ERROR_EVENT'
    ELSE 'UNKNOWN'
END
WHERE incident_type IN ('failedExternalTask', 'unhandledErrorEvent', 'executionError');

INSERT INTO public.schema_migrations (version, description)
VALUES ('007', 'types d''incident de Camunda 8')
ON CONFLICT (version) DO NOTHING;
