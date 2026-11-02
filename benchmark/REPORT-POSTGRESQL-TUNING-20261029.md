# Benchmark PostgreSQL après indexation des FK parentes

Date : 2026-10-29.

Cette campagne valide le schéma final dans lequel les repositories écrivent
directement les FK `BIGINT`, les colonnes et triggers UUID de compatibilité ont
été supprimés des entités chaudes, et les FK consultées par l'archivage sont
indexées par la migration `018_parent_fk_indexes.sql`.

## Environnement et validation

- Base et volumes Docker recréés avant la mesure ; version de schéma `018`.
- Seuls `bpmngo-benchmark-app` et `bpmngo-benchmark-postgres` actifs pendant
  la campagne, épinglés sur les CPU 0-1.
- Mémoire : 1 Gio pour PostgreSQL et 1,5 Gio pour BpmnGo.
- Deux passages à 10, 50 et 100 clients ; 10 s de chauffe et 60 s mesurées par
  palier.
- Zéro erreur sur les 78 548 instances mesurées.
- `go test ./...` passe intégralement.
- Les configurations Compose standard, scale et Citus sont valides.
- La validation fonctionnelle exécutée avant cette campagne reste verte :
  47/47 scénarios sur PostgreSQL et 47/47 sur le cluster Citus réel.

Les résultats bruts autoritatifs sont dans
`benchmark/raw/postgresql-fk-indexed-final-20261029/`.

## Résultats de charge

| Clients | Passage | inst/s | p50 ms | p95 ms | Erreurs | CPU moyen app/PG | CPU/instance | Mémoire pic |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10 | 1 | 164,03 | 57,50 | 90,36 | 0 | 60,5 / 130,0 % | 11,61 ms | 322 Mio |
| 10 | 2 | 172,86 | 54,69 | 85,68 | 0 | 60,5 / 131,1 % | 11,08 ms | 599 Mio |
| 50 | 1 | 195,66 | 233,65 | 482,33 | 0 | 59,7 / 131,7 % | 9,79 ms | 513 Mio |
| 50 | 2 | 189,55 | 243,17 | 487,90 | 0 | 60,2 / 132,2 % | 10,15 ms | 678 Mio |
| 100 | 1 | 201,62 | 465,83 | 878,84 | 0 | 59,9 / 123,7 % | 9,11 ms | 562 Mio |
| 100 | 2 | 200,60 | 469,55 | 882,15 | 0 | 58,6 / 128,2 % | 9,31 ms | 684 Mio |

Comparaison à la campagne PostgreSQL réglée mais sans les index FK, sur la
même limite CPU et les mêmes limites mémoire :

| Clients | Avant inst/s | Après inst/s | Débit | p50 | p95 |
|---:|---:|---:|---:|---:|---:|
| 10 | 167,94 | 168,44 | +0,3 % | -0,3 % | -1,7 % |
| 50 | 190,03 | 192,61 | +1,4 % | -0,8 % | -1,4 % |
| 100 | 193,69 | 201,11 | +3,8 % | -3,9 % | -3,7 % |

Le gain de débit est modeste sur 60 secondes, mais augmente avec la
concurrence. Le principal bénéfice est la suppression du coût croissant de
l'archivage lorsque les tables grossissent.

## Cause CPU confirmée par `pg_stat_statements`

Avant `018`, supprimer une instance déclenchait trois recherches de FK sans
index qui scannaient les tables chaudes. Après `018` :

| Opération | Avant, moyenne | Après, moyenne | Amélioration |
|---|---:|---:|---:|
| `DELETE process_instances` | 12,097 ms | 0,333 ms | 36,3× |
| FK `process_instances.parent_process_instance_key` | 3,726 ms | 0,0014 ms | 2 661× |
| FK `executions.parent_execution_key` | 3,660 ms | 0,0014 ms | 2 614× |
| FK `process_instances.parent_execution_key` | 3,569 ms | 0,0011 ms | 3 245× |

Après la charge, l'archiver avait déjà supprimé 38 975 instances et il ne
restait que 39 576 instances terminées, sans instance en cours. L'autovacuum
et l'auto-analyze se sont exécutés huit fois sur chacune des tables chaudes
principales, ce qui confirme que les paramètres par table sont actifs.

## Améliorations PostgreSQL suivantes

1. Éliminer les recherches répétées `SELECT engine_key FROM
   process_instances WHERE id = $1` : 2 670 692 appels pendant cette campagne.
   Propager la clé `BIGINT` dans le contexte runtime réduira les allers-retours
   SQL, même si chaque recherche ne prend actuellement que 0,0053 ms.
2. Examiner l'insertion des jobs, première requête par temps cumulé (21,7 s),
   puis tester des lots ou une réduction des index redondants avec
   `EXPLAIN (ANALYZE, BUFFERS, WAL)`.
3. Mesurer les écritures WAL, les checkpoints et le cache avec
   `pg_stat_wal`, `pg_stat_bgwriter` et `pg_stat_io` sur une campagne longue.
   Le test court sature encore presque deux CPU, mais plus la mémoire.
4. Conserver les partitions mensuelles d'historique/audit et automatiser leur
   création anticipée, leur détachement et leur purge selon la rétention.
5. Déployer PgBouncer en mode transaction lorsque plusieurs réplicas BpmnGo
   partagent PostgreSQL, puis dimensionner le pool Go sur le nombre réel de
   connexions serveur.
6. Ajouter une campagne d'endurance de 30 à 60 minutes. Elle permettra de
   mesurer le bloat, l'autovacuum, la stabilité du débit et l'effet des
   partitions au-delà du test synthétique de six minutes.

## Conclusion

La migration fonctionnelle et la correction PostgreSQL sont validées. Les FK
chaudes sont écrites directement en `BIGINT`, les compatibilités UUID ont été
retirées, et les scans de FK responsables du coût d'archivage ont disparu. La
priorité suivante est de supprimer les résolutions UUID→BIGINT répétées dans
le runtime, puis de profiler WAL/checkpoints sur une campagne d'endurance.
