# Benchmark de la feuille de route transactionnelle

Date : 2026-10-29.

Cette campagne valide les premiers lots transactionnels : contexte runtime
consolidé, `NOTIFY` atomiques, réclamation de jobs par `UPDATE ... RETURNING`,
hydratation batch après commit, unit-of-work service→service, audit groupé,
admission runtime et caches d'autorisation.

## Environnement

- Seuls `bpmngo-benchmark-app` et `bpmngo-benchmark-postgres` tournaient.
- Les deux conteneurs partageaient les CPU `0-1`.
- Budget mémoire total strict de 2 Gio : application 1 280 Mio, PostgreSQL
  768 Mio.
- `RUNTIME_MAX_CONCURRENCY=8`, mode d'écriture `direct`, 8 workers par type,
  lots de 32.
- Chaque palier comprend 10 s de chauffe puis 60 s mesurées.
- La suite `go test ./...` passe intégralement.
- Les résultats bruts sont dans
  `benchmark/raw/postgresql-transaction-roadmap-20261029/`.

## Résultats de charge

| Clients | Passage | inst/s | p50 ms | p95 ms | p99 ms | Erreurs | CPU app/PG | CPU/instance | Mémoire pic totale |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10 | 1 | 233,41 | 40,84 | 61,44 | 74,72 | 0 | 57,8 / 133,8 % | 8,21 ms | 354 Mio |
| 10 | 2 | 236,42 | 39,94 | 62,04 | 76,51 | 0 | 57,0 / 134,6 % | 8,10 ms | 657 Mio |
| 50 | 1 | 284,53 | 159,33 | 336,03 | 435,57 | 0 | 56,4 / 135,6 % | 6,75 ms | 585 Mio |
| 50 | 2 | 276,74 | 163,19 | 342,44 | 442,85 | 0 | 55,5 / 135,7 % | 6,91 ms | 689 Mio |
| 100 | 1 | 285,85 | 328,59 | 637,09 | 771,36 | 0 | 55,7 / 135,9 % | 6,70 ms | 654 Mio |
| 100 | 2 | 280,84 | 335,24 | 647,93 | 780,96 | 0 | 56,0 / 136,9 % | 6,87 ms | 729 Mio |

La variance de débit entre les deux passages reste comprise entre 1,3 % et
2,7 %, très loin du seuil de 20 %. Les 96 074 instances mesurées se sont
terminées sans erreur.

## Comparaison avec la campagne FK indexées

| Clients | Avant, moyenne inst/s | Après, moyenne inst/s | Gain |
|---:|---:|---:|---:|
| 10 | 168,44 | 234,91 | +39,5 % |
| 50 | 192,61 | 280,63 | +45,7 % |
| 100 | 201,11 | 283,34 | +40,9 % |

Le CPU total reste proche de deux cœurs, ce qui est normal sous une charge
fermée qui cherche le débit maximal. L'amélioration se voit dans le travail
utile par unité de CPU : à 100 clients, environ 6,8 ms CPU/instance contre
9,2 ms auparavant.

## Transactions et WAL

`pg_stat_database` a relevé 3 197 166 commits et 21 491 rollbacks sur toute
la session, chauffes et archivage compris. Le ratio brut est nettement plus
bas que les ~43 commits/autocommits par instance de l'endurance précédente,
mais il ne doit pas être divisé uniquement par les instances mesurées car les
six chauffes font aussi du travail.

Attribution du WAL par famille SQL sur la session :

| Famille | WAL |
|---|---:|
| Audit batch | 841,4 Mo |
| Jobs | 375,5 Mo |
| Archive/historique | 338,0 Mo |
| Exécutions | 270,4 Mo |
| Instances | 173,4 Mo |
| Variables/portées | 170,0 Mo |
| Autres | 166,3 Mo |

L'audit reste la première source de WAL, mais ses 9 754 insertions batch
remplacent plusieurs millions d'insertions unitaires. La nouvelle capture
`wal_by_operation.csv` rend cette répartition reproductible.

## Validation fonctionnelle ciblée

- Smoke latence : cinq appels de chaque type, aucune erreur.
- Smoke unit-of-work à 50 clients : 9 915 instances en 15,08 s,
  657,45 inst/s hors limites CPU du benchmark, aucune erreur.
- Les métriques `goflow_engine_transaction_*`,
  `goflow_runtime_admission_wait_seconds` et
  `goflow_runtime_executions_in_flight` sont exposées et alimentées.
- Les conteneurs arrêtés pour l'isolation ont été restaurés après la campagne.

## Conclusion

La réduction du nombre de transactions et des lectures N+1 apporte un gain
reproductible de 40 à 46 % selon le niveau de concurrence, sans erreur et
sans dépasser 2 Gio. PostgreSQL reste le principal consommateur CPU et
l'audit la première source de WAL ; les prochaines mesures longues devront
donc surveiller le bloat, les checkpoints et la vitesse de rattrapage de
l'archiveur adaptatif.
