-- 012_message_start_bigint_reservation.sql
-- Fix databases that applied the first revision of migration 011. A message
-- start reserves its correlation before the process instance exists, so its
-- BIGINT instance FK must be nullable for that short reservation window.

ALTER TABLE public.published_message_correlations
  ALTER COLUMN process_instance_key DROP NOT NULL;

INSERT INTO public.schema_migrations (version, description)
VALUES ('012', 'corrélation message-start: FK BIGINT nullable pendant la réservation')
ON CONFLICT (version) DO NOTHING;
