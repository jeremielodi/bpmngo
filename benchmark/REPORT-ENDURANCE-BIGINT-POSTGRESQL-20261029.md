# Endurance PostgreSQL et propagation BIGINT

Date : 2026-10-29. Cette campagne valide les six optimisations PostgreSQL
suivantes sur une base neuve en schéma `019`, isolée sur deux CPU.

## Résumé

- 419 428 instances terminées pendant les 30 minutes mesurées, soit
  **232,98 instances/s**, sans erreur.
- p50 396,9 ms, p95 779,9 ms, p99 1 001,6 ms.
- CPU moyen 192,6 % : application 56,3 %, PostgreSQL 136,4 % ;
  **8,27 ms CPU par instance**.
- Mémoire maximale : application 163 Mio, PostgreSQL 1 022 Mio.
- La même charge courte, après les deux dernières écritures BIGINT et sur la
  base déjà volumineuse, donne 233,27 instances/s, zéro erreur et p95 766,8 ms.

Résultats bruts :
`benchmark/raw/postgresql-bigint-context-endurance-20261029/endurance-30m-100c/`.

## 1. Résolution UUID vers BIGINT

Avant cette optimisation, le benchmark court comptait 2 670 692 appels de :

```sql
SELECT engine_key FROM process_instances WHERE id = $1
```

L'exécution, le job et la création d'instance transportent maintenant
`process_instance_key`. Un registre borné à 131 072 associations permet aux
API internes encore exprimées en UUID de réutiliser la clé déjà chargée ; il
n'est pas une source de vérité et une absence retombe sur PostgreSQL.

Pendant l'endurance :

- 434 819 instances ont été créées ;
- 435 044 résolutions ont été exécutées, soit **1,0005 résolution initiale
  par instance**, au lieu d'environ 34 ;
- toutes les utilisations suivantes de la même instance ont réutilisé la clé ;
- le test unitaire vérifie que 100 résolutions d'un même UUID ne produisent
  qu'une seule requête SQL ;
- le contrôle final montre les mises à jour de job et d'exécution directement
  routées par `process_instance_key`, sans sous-requête UUID.

Le coût de résolution passe donc d'environ 34 allers-retours par instance à
un seul lors des chemins de compatibilité, soit une réduction d'environ 97 %.

## 2. Insertion et index des jobs

Sur 869 638 insertions de jobs :

- temps moyen : 0,1289 ms ;
- WAL : 609 665 865 octets, soit environ 701 octets par job ;
- l'insertion est la première requête par temps cumulé uniquement parce
  qu'elle est appelée deux fois par instance, pas parce qu'une insertion
  individuelle est lente.

La comparaison intercalée `EXPLAIN (ANALYZE, BUFFERS, WAL)` avec et sans
`idx_jobs_pending_type_created`, chaque transaction étant annulée, mesure :

| Variante | Temps du nœud INSERT | WAL normal |
|---|---:|---:|
| Avec index partiel | 0,146–0,189 ms | 5 records, 463 octets |
| Sans index partiel | 0,093–0,114 ms | 4 records, 335 octets |

L'index coûte environ 0,06 ms et 128 octets WAL par insertion. Il est conservé
car le polling l'utilise en 0,042 ms sur une table proche de 700 000 jobs.
Les trois autres index sont la PK et les deux index de FK nécessaires aux
suppressions parentales ; aucun index jobs n'est redondant. Un insert en lot
n'est pas approprié au chemin principal : chaque job est un état d'attente
durable qui doit devenir récupérable immédiatement après le commit du token.

## 3. WAL, checkpoints, cache et bloat

Sur la fenêtre complète, chauffe comprise :

- WAL : 8 791 262 566 octets, soit environ 20,2 Kio par instance créée et
  4,7 Mio/s ;
- 78 459 868 enregistrements WAL et 266 859 full-page images ;
- 7 checkpoints demandés par le volume WAL, aucun checkpoint seulement
  temporel ;
- 3 127 632 blocs lus et 514 759 534 hits, soit environ **99,40 % de cache** ;
- aucun fichier temporaire et aucun deadlock ;
- 31 autovacuums sur `jobs`, `executions` et `variables` ;
- tuples morts en fin de charge : jobs 5,36 %, variables 3,56 %,
  process_instances 6,48 %, executions 10,70 %.

La mémoire n'est plus le premier goulot. Les prochains gains viendront du
nombre de transactions, du WAL et de l'archiveur, non d'une hausse aveugle de
`work_mem`.

## 4. Partitions et rétention

La migration `019_partition_retention.sql` ajoute
`goflow_drop_empty_history_partitions(cutoff)`. Après la purge par instance,
elle :

1. sélectionne uniquement les partitions mensuelles dont la borne supérieure
   est expirée ;
2. verrouille la partition contre une insertion d'archive tardive ;
3. la détache et la supprime uniquement si elle est réellement vide.

Cette condition protège les audits non liés à une instance et les activités
d'un processus long dont le mois d'activité diffère du mois de fin. Le test
réel a créé, détaché et supprimé une partition expirée vide. La création de
douze mois futurs continue quotidiennement via
`goflow_ensure_history_partitions(12)`.

## 5. PgBouncer et pools

Le manifeste scale déploie PgBouncer 1.26 en mode transaction. Les valeurs par
défaut sont maintenant :

- 40 connexions ouvertes et 10 inactives par réplique BpmnGo ;
- 80 connexions serveur dans PgBouncer ;
- avec trois répliques : 120 clients possibles multiplexés sur 80 sessions
  PostgreSQL.

Les travaux exigeant une session (`LISTEN`, verrous consultatifs de rétention
et d'archivage) continuent à passer directement par PostgreSQL. La procédure
de dimensionnement et `SHOW POOLS`/`cl_waiting` sont documentés.

## 6. Limite révélée par l'endurance

Le débit reste stable malgré la croissance des tables, ce qui confirme les
index de FK de la migration `018`. En revanche, sous deux CPU saturés :

- 434 819 instances ont été créées ;
- 85 546 ont été archivées pendant la charge ;
- environ 349 000 restaient dans les tables chaudes au snapshot final ;
- l'archiveur traite environ 46 instances/s sous charge, puis environ
  350 instances/s une fois la charge arrêtée.

L'archiveur n'est donc pas lent intrinsèquement : il est privé de CPU/I/O par
le chemin transactionnel. La prochaine optimisation doit lui donner une
limite de concurrence et des lots adaptatifs, ou réserver des ressources,
plutôt que multiplier sans borne les workers. Il faut également réduire les
transactions d'audit/archive et mesurer l'effet sur les 20,2 Kio de WAL par
instance.

## Validation

- `go test ./...` : vert ;
- schéma neuf : version `019` ;
- migration de détachement testée réellement ;
- configuration Compose standard, scale et Citus valide ;
- benchmark d'endurance : code de sortie 0, zéro erreur ;
- contrôle post-endurance sur base volumineuse : 233,27 instances/s, zéro
  erreur, requêtes de complétion directement en BIGINT.
