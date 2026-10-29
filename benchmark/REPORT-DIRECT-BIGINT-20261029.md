# Benchmark BpmnGo après suppression des UUID chauds

Date : 2026-10-29. Cette campagne mesure le schéma final dans lequel les
repositories écrivent directement les relations `BIGINT` et où les UUID de
compatibilité ont été retirés de `executions`, `jobs`, `tasks`, `timer_jobs`,
`incidents` et `variables`.

## Environnement

- Pile Docker neuve et isolée : seuls `bpmngo-benchmark-app` et
  `bpmngo-benchmark-postgres` étaient actifs pendant les mesures.
- CPU partagés : CPU 0-1. Limites mémoire : application 1 536 Mio,
  PostgreSQL 512 Mio.
- Mode d'écriture `direct`, 8 workers par type, lots de 32.
- Chauffe : 10 s puis 120 s à 50 clients : 27 421 instances, 228,23 inst/s,
  zéro erreur.
- Tous les résultats ci-dessous utilisent exclusivement
  `benchmark/raw/direct-bigint-final-20261029/`.

## Validation fonctionnelle et SQL

- 47/47 scénarios exécutés sans erreur.
- Comme avant cette migration, 46/47 observations normalisées sont identiques
  à la référence Camunda : l'historique des éléments d'une instance terminée
  reste l'écart connu.
- La suite Go complète passe : `go test -mod=vendor ./...`.
- La compilation complète passe : `go build -mod=vendor ./...`.
- Les tables chaudes ne contiennent plus de colonnes UUID d'identité ou de
  relation.
- Les triggers de double écriture ont disparu. Restent quatre triggers
  d'identité sur les racines et deux triggers `NOTIFY` de variables.
- `published_message_correlations` utilise la PK
  `(published_message_key, process_key)` et ne duplique plus l'UUID du message.

## Latence séquentielle

Chaque cellule donne p50/p95 en millisecondes. Il y a 30 appels par opération
et par passage, sans erreur.

| Appel | Passage 1 | Passage 2 |
|---|---:|---:|
| `Topology` | 0,321 / 1,107 | 0,346 / 0,545 |
| `DeployResource` | 1,633 / 2,071 | 1,427 / 3,853 |
| `CreateProcessInstance` | 2,584 / 5,355 | 2,709 / 3,148 |
| `CreateProcessInstanceWithResult` | 2,669 / 3,672 | 2,815 / 5,829 |
| Création → réception du job | 3,005 / 6,500 | 3,218 / 4,012 |
| `CompleteJob` | 2,290 / 5,448 | 2,640 / 3,686 |

Les médianes restent très proches de la version hybride. Les p95 alternent
entre mieux et moins bien selon le passage : cette série courte ne démontre
pas à elle seule un gain de latence.

## Charge

Processus début → deux tâches de service → fin. Chaque palier comprend 10 s de
chauffe et 60 s mesurées. CPU moyen = application + PostgreSQL; la parenthèse
donne leur répartition. CPU/instance est dérivé du CPU moyen échantillonné.

| Clients | Passage | inst/s | p50 ms | p95 ms | p99 ms | Erreurs | CPU moy. (app/PG) | CPU/inst. | Mémoire pic |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10 | 1 | 193,64 | 48,69 | 78,82 | 97,08 | 0 | 183,2 % (54,2/129,0) | 9,46 ms | 558 Mio |
| 10 | 2 | 170,28 | 54,91 | 92,98 | 115,55 | 0 | 183,0 % (52,8/130,2) | 10,75 ms | 575 Mio |
| 50 | 1 | 212,10 | 211,75 | 465,80 | 611,76 | 0 | 192,1 % (51,5/140,6) | 9,06 ms | 589 Mio |
| 50 | 2 | 190,34 | 233,88 | 529,43 | 728,57 | 0 | 192,8 % (51,3/141,6) | 10,13 ms | 651 Mio |
| 100 | 1 | 208,48 | 451,50 | 879,87 | 1 072,37 | 0 | 193,3 % (53,4/139,8) | 9,27 ms | 572 Mio |
| 100 | 2 | 204,95 | 455,07 | 908,74 | 1 091,71 | 0 | 191,4 % (51,6/139,8) | 9,34 ms | 602 Mio |

Comparaison des moyennes avec le schéma hybride 009-012 :

| Clients | Hybride inst/s | BIGINT direct inst/s | Variation | Variation p50 | Variation p95 |
|---:|---:|---:|---:|---:|---:|
| 10 | 180,36 | 181,96 | +0,9 % | -1,4 % | -1,2 % |
| 50 | 221,09 | 201,22 | -9,0 % | +8,4 % | +14,0 % |
| 100 | 221,99 | 206,72 | -6,9 % | +7,7 % | +8,9 % |

Le résultat important est l'absence d'erreur et la disparition de la double
écriture, mais cette campagne **ne prouve pas encore un gain de débit**. À
50/100 clients elle mesure même un recul de 7 à 9 %. PostgreSQL consomme
environ 130 à 142 % de CPU et sa limite de 512 Mio est atteinte; le prochain
diagnostic doit donc porter sur les plans SQL, les index et la pression mémoire,
avec plusieurs répétitions plus longues avant d'attribuer l'écart aux clés.

## Prochaines optimisations PostgreSQL

1. Activer `pg_stat_statements` puis capturer les requêtes qui consomment le
   plus de temps total pendant une charge reproductible. Le benchmark actuel
   n'a que `plpgsql` et `pgcrypto` activés.
2. Remplacer les index de polling génériques par des index partiels adaptés :
   jobs en attente par type et date de création, timers non déclenchés par
   `due_at`, incidents ouverts. Valider chaque remplacement avec
   `EXPLAIN (ANALYZE, BUFFERS)`.
3. Supprimer les index redondants après mesure. Exemple observé :
   `variables(process_instance_key)` existe à la fois en index simple et en
   index unique; les timers cumulent trois index qui se recouvrent sur
   `due_at`/`is_triggered`.
4. Régler l'autovacuum table par table sur les tables très mises à jour
   (`jobs`, `executions`, `variables`, `audit_logs`) avec des scale factors
   plus faibles. Après la campagne, `audit_logs` comptait environ 162 000
   tuples morts pour 987 000 vivants.
5. Partitionner les tables historiques et `audit_logs` par mois, puis détacher
   ou supprimer les anciennes partitions selon la rétention. Un index BRIN sur
   leur timestamp devient intéressant quand ces tables grandissent.
6. Donner davantage de mémoire à PostgreSQL avant une nouvelle comparaison :
   la limite de 512 Mio était saturée pendant la charge. Ajuster ensuite
   `shared_buffers`, `work_mem` et les checkpoints à partir des statistiques,
   pas à l'aveugle.
7. Mettre PgBouncer en mode transaction si plusieurs réplicas BpmnGo partagent
   PostgreSQL, et aligner le pool Go sur la capacité réelle de la base.
8. Reconcevoir le bootstrap Citus avant de réactiver `CITUS_ENABLED` : il fait
   encore référence aux anciennes colonnes UUID de distribution. PostgreSQL
   mono-nœud est le chemin validé par cette campagne.

## Conclusion

La migration fonctionnelle est validée : écritures directes BIGINT, UUID
chauds supprimés, 47 scénarios sans erreur et suite Go verte. La prochaine
priorité n'est pas une nouvelle forme d'identifiant, mais l'observation SQL
(`pg_stat_statements`) puis la consolidation des index de polling et de
l'autovacuum. Les résultats bruts sont conservés pour permettre une nouvelle
comparaison après ces réglages.
