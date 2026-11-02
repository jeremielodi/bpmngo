# Benchmark BpmnGo après migration BIGINT + UUIDv7

Date : 2026-10-29. Ce test compare la version courante à son rerun BpmnGo
précédent (`REPORT-RERUN-20261029.md`), sur la même machine et avec le même
harness public gRPC Zeebe + REST `/v2`.

## Environnement

- Pile Docker neuve et isolée, seuls `bpmngo-benchmark-app` et
  `bpmngo-benchmark-postgres` actifs pendant les mesures.
- CPU partagés : CPU 0-1. Mémoire : application 1 536 Mio, PostgreSQL 512 Mio.
- Mode d'écriture `direct`, 8 workers par type, lots de 32.
- Chauffe : 10 s puis 120 s à 50 clients : 28 947 instances, 240,94 inst/s,
  zéro erreur.
- Schéma : migrations 009 à 012, PK/FK BIGINT et UUIDv7 sur les quatre racines.

## Comportement

Les 47 scénarios se déploient et s'exécutent sans erreur. Par rapport à la
référence Camunda déjà capturée, l'écart historique demeure : la recherche des
éléments d'une instance terminée ne renvoie que l'événement de fin. Le résultat
reste donc **46/47 identiques à Camunda**, sans nouvelle incompatibilité liée
aux identifiants.

Le premier passage a révélé puis permis de corriger une régression de la
migration 011 : la réservation d'un démarrage par message précède la création
de l'instance, donc `published_message_correlations.process_instance_key` doit
être temporairement nullable. La migration 012 corrige ce point. Après
correction, le scénario crée une instance et livre bien le job
`message-started`.

| Mesure comportement | Avant | Après BIGINT |
|---|---:|---:|
| Durée | 21 s | 22 s |
| Temps CPU application | 8,86 s | 0,87 s |
| Temps CPU PostgreSQL | 12,66 s | 0,87 s |
| Temps CPU total | 21,53 s | 1,74 s |
| Mémoire pic | 528 Mio | 500 Mio |

La réduction CPU sur cette suite est de 91,9 %. Elle correspond notamment à
la suppression des traductions répétées via `zeebe_keys`; cette mesure courte
reste plus sensible au travail d'arrière-plan que les paliers de charge.

## Latence séquentielle

Chaque cellule donne p50/p95 en millisecondes, passage 1 puis passage 2,
30 appels par passage et zéro erreur.

| Appel | Avant | Après BIGINT |
|---|---:|---:|
| `Topology` | 0,355/0,522 · 0,355/0,534 | 0,386/1,416 · 0,335/0,388 |
| `DeployResource` | 1,794/4,397 · 1,685/2,224 | 1,638/3,474 · 1,434/1,957 |
| `CreateProcessInstance` | 3,261/4,051 · 3,887/5,783 | 2,490/3,069 · 2,790/4,290 |
| `CreateProcessInstanceWithResult` | 3,452/4,724 · 3,428/5,591 | 2,617/2,873 · 2,671/2,972 |
| Création → réception du job | 4,895/6,290 · 5,853/7,670 | 3,413/4,198 · 3,254/4,048 |
| `CompleteJob` | 2,201/3,957 · 2,262/3,126 | 2,339/2,692 · 2,217/2,633 |

Les créations et la livraison de jobs gagnent environ 20 à 40 % sur leurs
médianes. `CompleteJob` reste stable en p50 et améliore sa queue p95.

## Charge

Processus début → deux tâches de service → fin. Chaque palier comprend 10 s de
chauffe et 60 s mesurées. CPU moyen = application + PostgreSQL; la parenthèse
donne leur répartition. CPU/instance est dérivé du CPU moyen et du débit.

| Clients | Passage | inst/s | p50 ms | p95 ms | p99 ms | Erreurs | CPU moy. (app/PG) | CPU/inst. | Mémoire pic |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 10 | 1 | 172,21 | 54,94 | 91,30 | 114,15 | 0 | 192,2 % (53,3/138,9) | 11,16 ms | 506 Mio |
| 10 | 2 | 188,51 | 50,09 | 82,50 | 102,19 | 0 | 190,7 % (56,8/133,9) | 10,12 ms | 534 Mio |
| 50 | 1 | 226,64 | 202,57 | 419,50 | 530,04 | 0 | 193,3 % (61,4/131,9) | 8,53 ms | 548 Mio |
| 50 | 2 | 215,55 | 208,37 | 453,74 | 594,76 | 0 | 194,0 % (58,0/136,0) | 9,00 ms | 561 Mio |
| 100 | 1 | 216,22 | 432,09 | 838,68 | 1 032,50 | 0 | 193,7 % (60,1/133,6) | 8,96 ms | 498 Mio |
| 100 | 2 | 227,75 | 409,78 | 804,28 | 981,10 | 0 | 194,6 % (58,2/136,4) | 8,54 ms | 527 Mio |

Comparaison des moyennes avec le rerun précédent :

| Clients | Débit avant | Débit après | Variation | p50 moyen | p95 moyen |
|---:|---:|---:|---:|---:|---:|
| 10 | 208,60 | 180,36 | −13,5 % | +19,3 % | +18,3 % |
| 50 | 217,88 | 221,09 | +1,5 % | +3,7 % | −1,0 % |
| 100 | 222,26 | 221,99 | −0,1 % | +0,9 % | −6,2 % |

Les deux passes ne présentent aucun écart supérieur à 20 % sur le débit ou les
percentiles de charge, contrairement au rerun précédent. Le coût CPU moyen par
instance augmente toutefois d'environ 14 % : les triggers de double écriture
UUID/BIGINT déplacent davantage de travail vers PostgreSQL, qui consomme ici
environ 134 à 139 % de CPU.

## Conclusion

- Compatibilité : aucune nouvelle divergence; la régression message-start
  trouvée par le benchmark est corrigée par la migration 012.
- Latence isolée : amélioration nette des créations et de la livraison de job.
- Charge : stable à 50/100 clients, mais recul moyen de 13,5 % à 10 clients.
- CPU : forte amélioration sur la suite fonctionnelle, mais coût par instance
  supérieur sous charge tant que les triggers de double écriture restent actifs.
- Étape d'optimisation recommandée : faire écrire directement toutes les FK
  BIGINT par les repositories, puis retirer les triggers et colonnes UUID de
  compatibilité des entités chaudes.

Sorties brutes : `benchmark/raw/hybrid-keys-20261029/`.
