-- 022_audit_writer_marks.sql
-- Les lignes d'audit sont écrites par lots (toutes les 50 ms) par chaque
-- instance BpmnGo. Une lecture de l'audit faite sur une instance doit voir
-- les lignes encore en file sur les autres : step back, historique des
-- jetons, archivage. Chaque écrivain note ici, à chaque lot, l'heure
-- (horloge de PostgreSQL) jusqu'à laquelle tout ce qu'il avait en file est
-- écrit ; un lecteur attend que chaque écrivain vivant ait dépassé l'heure
-- de début de sa lecture.
--
-- UNLOGGED : ces marques n'ont de sens que pour les processus vivants, elles
-- ne produisent pas de WAL et une table vidée après un crash se remplit au
-- lot suivant.

BEGIN;

CREATE UNLOGGED TABLE IF NOT EXISTS public.audit_writer_marks (
    node          INTEGER PRIMARY KEY,
    flushed_until TIMESTAMPTZ NOT NULL
);

INSERT INTO public.schema_migrations (version, description)
VALUES ('022', 'audit_writer_marks : lecture de l audit cohérente entre instances BpmnGo')
ON CONFLICT (version) DO NOTHING;

COMMIT;
