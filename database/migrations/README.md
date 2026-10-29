# Migrations

Les migrations sont des fichiers SQL **exécutés à la main** sur le serveur.
L'application ne modifie jamais le schéma toute seule.

## Point de départ

- `database/01_schema.sql` est le schéma complet. Une base neuve créée avec
  ce fichier n'a besoin d'aucune migration.
- `000_baseline.sql` marque une base existante (la prod) comme étant à jour
  avec ce schéma. Les anciennes migrations 002 à 014, déjà incluses dans
  `01_schema.sql`, sont archivées dans
  `database/archive/migrations-before-baseline/`.
- La table `schema_migrations` garde la liste des migrations appliquées :

  ```sql
  SELECT * FROM schema_migrations ORDER BY version;
  ```

### Mise en place sur la prod (une seule fois)

```bash
# 1. Vérifier que la base contient tout (lecture seule) : toutes les lignes doivent être OK
psql -h <hôte> -U <user> -d <base> -f check_baseline.sql

# 2. Enregistrer la baseline
psql -h <hôte> -U <user> -d <base> -v ON_ERROR_STOP=1 -1 -f 000_baseline.sql
```

Si une ligne affiche `MANQUANT`, rejouer d'abord la migration archivée
correspondante (elles sont toutes idempotentes), puis relancer la
vérification.

## Migrations à appliquer

Dans l'ordre, après la baseline, chacune une seule fois. La plupart sont
idempotentes ; la migration de partitionnement 015 doit impérativement être
pilotée par `schema_migrations` et ne pas être rejouée. Toutes ajoutent
sans rien modifier aux données existantes, sauf 007 qui renomme les types
des incidents déjà enregistrés.

| Version | Fichier | Ce qu'elle ajoute | Pour |
|---|---|---|---|
| 001 | `001_published_messages.sql` | tables `published_messages`, `published_message_correlations` | messages Camunda 8 (durée de vie, déduplication) |
| 002 | `002_scope_variables.sql` | table `scope_variables` | variables locales (sous-processus, multi-instance) |
| 003 | `003_incident_execution.sql` | colonne `incidents.execution_id` | incidents qui relancent l'élément à leur résolution |
| 004 | `004_variables_notify.sql` | fonction et triggers `goflow_notify_variables` | événements conditionnels réévalués à chaque écriture de variable |
| 005 | `005_hot_path_indexes.sql` | index sur `jobs`, `timer_jobs`, `event_subscriptions`… | performance avec beaucoup d'instances vivantes |
| 006 | `006_business_key.sql` | colonne `business_key` sur `process_instances` et `historic_process_instances` | business key des applications Camunda 7 |
| 007 | `007_incident_types.sql` | renomme `incident_type` des incidents existants (live et archivés) en types Camunda 8 | types d'incident de Camunda 8 (`/v2`, gRPC) |
| 008 | `008_id_indexes.sql` | index sur `executions(id)`, `incidents(id)`, `incidents(job_id)` | performance sous charge (recherches par identifiant seul) |
| 009 | `009_hybrid_identifiers_phase1.sql` | identités BIGINT futures et UUID globales, avec backfill des clés Zeebe existantes | migration hybride sans renumérotation client |
| 010 | `010_hybrid_identifiers_primary_keys.sql` | promotion des `engine_key` en PK physiques; UUID legacy conservés comme uniques pendant le rolling upgrade | nécessite 009 |
| 011 | `011_hybrid_identifiers_foreign_keys.sql` | graphe de relations BIGINT, backfill et triggers de double écriture | nécessite 010 |
| 012 | `012_message_start_bigint_reservation.sql` | autorise la réservation message-start avant création de l'instance | correctif de 011 |
| 013 | `013_direct_bigint_hot_relationships.sql` | écrit les relations runtime directement en BIGINT, migre les tables auxiliaires et retire les UUID/triggers de compatibilité des entités chaudes | nécessite 009–012; arrêt des anciens binaires avant application |
| 014 | `014_postgresql_runtime_tuning.sql` | active `pg_stat_statements`, remplace les index de polling redondants par des index partiels et règle l'autovacuum des tables chaudes | redémarrer PostgreSQL avec `shared_preload_libraries=pg_stat_statements` |
| 015 | `015_monthly_history_partitions.sql` | convertit les tables `historic_*` et `audit_logs` en partitions mensuelles et prépare douze mois | fenêtre de maintenance : copie et verrouille les tables existantes |
| 016 | `016_gateway_state_bigint.sql` | remplace la dernière relation UUID de `gateway_state` par `process_instance_key BIGINT` | nécessite 013; prépare la colocation Citus |
| 017 | `017_remove_identity_compatibility.sql` | retire les `uid` dupliqués, les triggers d'identité de rolling upgrade et `executions.gateway_join_id` inutilisé | les racines gardent `BIGINT PK + id UUIDv7 UNIQUE`; les entités chaudes restent BIGINT uniquement |
| 018 | `018_parent_fk_indexes.sql` | indexe les FK parentes utilisées par `ON DELETE SET NULL/CASCADE` | évite trois scans complets par instance pendant l'archivage |
| 019 | `019_partition_retention.sql` | détache et supprime les partitions historiques expirées devenues vides | la purge ligne par ligne reste l'autorité pour ne pas perdre les audits non liés |
| 020 | `020_simulation_lab.sql` | scénarios BPMN, campagnes de simulation et registre de problèmes | laboratoire d'analyse, dimensionnement et redesign |
| 021 | `021_simulation_async_sweeps.sql` | file asynchrone des simulations, progression, annulation et campagnes de capacité | nécessite 020 |
| 022 | `022_simulation_multi_parameter_experiments.sql` | expériences de simulation multi-paramètres | nécessite 021 |

Pour voir où en est une base :

```sql
SELECT version, description, applied_at FROM schema_migrations ORDER BY version;
```

Une base neuve créée avec `database/01_schema.sql` les contient déjà
toutes (et le cluster haute disponibilité aussi, via `deploy/ha/ha-init.sh`).

## Écrire une nouvelle migration

1. Créer `NNN_description_courte.sql` avec le numéro suivant (`001_...`,
   `002_...`).
2. Rendre le fichier idempotent autant que possible (`IF NOT EXISTS`,
   `IF EXISTS`), pour qu'une exécution en double ne casse rien.
3. Terminer le fichier par son enregistrement :

   ```sql
   INSERT INTO public.schema_migrations (version, description)
   VALUES ('001', 'description courte')
   ON CONFLICT (version) DO NOTHING;
   ```

4. Reporter le même changement dans `database/01_schema.sql` (et l'INSERT
   dans `schema_migrations` à la fin du fichier), pour qu'une base neuve
   soit identique à une base migrée.

## Appliquer une migration

```bash
# Toujours faire une sauvegarde avant
pg_dump -h <hôte> -U <user> -Fc <base> > backup_$(date +%Y%m%d_%H%M).dump

# -1 : tout le fichier dans une seule transaction (tout ou rien)
# ON_ERROR_STOP : s'arrête à la première erreur
psql -h <hôte> -U <user> -d <base> -v ON_ERROR_STOP=1 -1 -f 001_description_courte.sql
```

Exception : `CREATE INDEX CONCURRENTLY` ne peut pas tourner dans une
transaction. Une migration qui l'utilise s'exécute sans `-1`, et doit être
seule dans son fichier.

## Réglages serveur

`database/tuning/postgresql-tuning.sql` n'est pas une migration : il règle
le serveur Postgres (`ALTER SYSTEM`, mémoire, timeouts) et doit être adapté
à la machine avant d'être appliqué. Il ne s'exécute pas dans une
transaction (pas de `-1`).
