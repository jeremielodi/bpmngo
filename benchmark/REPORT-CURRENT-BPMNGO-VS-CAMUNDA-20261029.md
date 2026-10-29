# BpmnGo actuel face à Camunda 8.10.7

Date : 29 octobre 2026.

## Résumé exécutif

BpmnGo est désormais un moteur BPMN crédible pour une plateforme contrôlée,
particulièrement lorsque la faible consommation mémoire, PostgreSQL, le
self-hosting et la compatibilité conjointe Camunda 7/8 ont plus de valeur que
l'écosystème complet de Camunda. Il n'est cependant pas encore un remplacement
universel de Camunda 8.

Sur le dernier benchmark A/B strict, BpmnGo reproduit 46 scénarios sur 47,
utilise 68 à 72 % moins de mémoire sous charge et reste très rapide à faible
concurrence. Camunda garde une meilleure efficacité CPU, une latence médiane
plus faible sur la création séquentielle et un produit/écosystème beaucoup
plus complet.

Depuis cet A/B, les optimisations transactionnelles BpmnGo ont porté le débit
moyen à 235, 281 et 283 instances/s à 10, 50 et 100 clients. Comparées à la
baseline Camunda conservée, ces valeurs sont respectivement supérieures de
39 %, 20 % et 18 %. Cette comparaison est indicative : le BpmnGo actuel n'a
pas encore été rejoué dans une nouvelle campagne A/B complète contre une base
Camunda neuve.

## 1. Périmètre et hiérarchie des preuves

Le moteur de référence est Camunda 8.10.7 avec Connectors 8.10.2. Le système
BpmnGo actuel est le working tree postérieur à la release 1.0.1, avec :

- clés moteur BIGINT, UUIDv7 conservés sur les racines publiques ;
- propagation directe des clés BIGINT dans le runtime ;
- transactions consolidées, audit asynchrone groupé et `NOTIFY` atomiques ;
- admission dynamique selon le backlog d'archive ;
- archives regroupées jusqu'à dix instances par transaction ;
- PostgreSQL 16, partitions mensuelles et observabilité WAL ;
- modes direct, multi-réplique/PgBouncer et Citus disponibles.

Trois familles de résultats sont utilisées :

1. **A/B strict et directement comparable** : mêmes API publiques, modèles,
   limites de deux CPU/deux Gio et deux passages, rapport
   `REPORT-RERUN-20261029.md` ;
2. **BpmnGo actuel, post-optimisations** : mêmes processus, clients et limites,
   deux passages, mais sans nouveau passage Camunda simultané, rapport
   `REPORT-TRANSACTION-ROADMAP-20261029.md` ;
3. **endurance et archivage BpmnGo** : 30 minutes puis campagne ciblée de
   contention d'archive.

Cette séparation évite de présenter une extrapolation comme un nouveau duel
mesuré.

## 2. Conformité fonctionnelle

### Résultat mesuré

| Indicateur | Camunda 8.10.7 | BpmnGo |
|---|---:|---:|
| Modèles déployés | 47/47 | 47/47 |
| Scénarios terminés sans erreur du harness | 47/47 | 47/47 |
| Observations normalisées identiques, volume neuf | référence | 46/47 |
| Écart reproduit | aucun | historique immédiat incomplet |

L'écart restant du dernier A/B propre concernait
`finished-instance-elements-and-variables` : les variables étaient correctes,
mais la recherche historique immédiate ne renvoyait que l'élément `end`, au
lieu de `start`, `work`, `end`. Un ancien passage sur une base conservée avait
atteint 47/47, mais il ne remplace pas la reproduction sur volume neuf.

Les modifications ultérieures n'ont pas encore fait l'objet d'un nouveau
47/47 propre contre Camunda ; le statut prudent du système actuel reste donc
**46/47 prouvé**, et non 47/47.

### Couverture BPMN démontrée

BpmnGo couvre dans les tests :

- gateways exclusive, inclusive, parallèle et événementielle ;
- service tasks, user tasks, scripts FEEL et business rules DMN ;
- événements timer, message, signal, erreur, escalade, compensation et
  conditionnels ;
- événements de bord et event subprocesses interruptifs/non interruptifs ;
- call activities, sous-processus imbriqués et multi-instance parallèle ou
  séquentiel ;
- messages avec TTL, déduplication et clé de corrélation ;
- timers ISO 8601, cycles et cron ;
- scopes de variables et mappings d'entrée/sortie ;
- échecs de jobs, retries, backoff, incidents et résolution ;
- protocole Zeebe gRPC et un sous-ensemble REST `/v2`.

Quarante-sept scénarios sont une bonne régression ciblée, pas une preuve de
conformité complète à toute la spécification BPMN/FEEL ou à chaque version des
SDK Camunda.

## 3. Ressources du dernier A/B strict

Suite fonctionnelle complète :

| Moteur | Durée | CPU cumulé | CPU moyen | Pic CPU | Mémoire pic |
|---|---:|---:|---:|---:|---:|
| Camunda | 26 s | 7,48 s | 28,8 % | 77,8 % | 1 929 Mio |
| BpmnGo | 21 s | 21,53 s | 102,5 % | 107,6 % | 528 Mio |
| Rapport BpmnGo/Camunda | 0,81× | 2,88× | 3,56× | 1,38× | 0,27× |

BpmnGo termine 19 % plus vite et utilise 73 % moins de mémoire, mais consomme
2,88 fois plus de temps CPU sur cette suite. PostgreSQL représente 12,66 s sur
les 21,53 s CPU de BpmnGo.

Sous la charge A/B, BpmnGo utilisait 1,6 à 2,1 fois plus de CPU par instance,
mais seulement 28 à 32 % de la mémoire de Camunda. Avec les transactions
actuelles, l'efficacité BpmnGo mesurée est passée à environ 6,8 ms CPU/instance
à 100 clients, contre environ 4,2 ms pour Camunda dans la baseline : l'écart
indicatif se resserre autour de 1,6×, sans disparaître.

## 4. Latence séquentielle du dernier A/B

Chaque valeur représente passage 1 / passage 2, en millisecondes, trente
appels par passage.

| Appel | Camunda p50 / p95 | BpmnGo p50 / p95 | Avantage |
|---|---:|---:|---|
| Topology | 0,374/1,365 · 0,287/0,511 | 0,355/0,522 · 0,355/0,534 | proche |
| DeployResource | 5,587/18,516 · 4,562/7,576 | 1,794/4,397 · 1,685/2,224 | BpmnGo |
| CreateProcessInstance | 2,181/4,952 · 1,851/3,909 | 3,261/4,051 · 3,887/5,783 | Camunda en p50 |
| CreateWithResult | 2,027/3,319 · 1,677/3,470 | 3,452/4,724 · 3,428/5,591 | Camunda |
| Création → job | 4,479/11,618 · 2,716/7,639 | 4,895/6,290 · 5,853/7,670 | partagé |
| CompleteJob | 1,867/4,430 · 1,835/3,050 | 2,201/3,957 · 2,262/3,126 | proche, Camunda p50 |

BpmnGo déploie nettement plus vite. Camunda reste meilleur sur le chemin
séquentiel de création ; BpmnGo a parfois une meilleure queue p95 de livraison
de job.

## 5. Charge A/B stricte

Processus : début → deux tâches de service → fin. Huit workers, lots de 32,
dix secondes de chauffe et soixante secondes mesurées.

| Clients | Moteur/passage | inst/s | p50 ms | p95 ms | p99 ms | Erreurs | CPU/inst. | Mémoire pic |
|---:|---|---:|---:|---:|---:|---:|---:|---:|
| 10 | Camunda P1 / P2 | 178,52 / 160,31 | 51,87 / 56,67 | 96,96 / 110,99 | 124,15 / 147,94 | 0 | 5,37 / 5,60 ms | 1 892 / 1 925 Mio |
| 10 | BpmnGo P1 / P2 | 218,20 / 198,99 | 43,03 / 44,99 | 69,66 / 77,20 | 85,67 / 134,78 | 0 | 8,48 / 8,79 ms | 533 / 530 Mio |
| 50 | Camunda P1 / P2 | 241,56 / 225,46 | 178,46 / 193,46 | 423,81 / 464,09 | 568,85 / 618,79 | 0 | 4,42 / 4,11 ms | 1 876 / 1 889 Mio |
| 50 | BpmnGo P1 / P2 | 237,35 / 198,41 | 191,23 / 205,18 | 407,25 / 474,63 | 526,23 / 936,17 | 0 | 7,74 / 8,47 ms | 542 / 528 Mio |
| 100 | Camunda P1 / P2 | 246,32 / 234,95 | 374,53 / 390,75 | 774,01 / 820,82 | 956,97 / 1 022,45 | 0 | 4,33 / 4,12 ms | 1 777 / 1 902 Mio |
| 100 | BpmnGo P1 / P2 | 247,69 / 196,84 | 378,71 / 455,97 | 724,52 / 1 026,98 | 881,15 / 1 380,00 | 0 | 7,39 / 8,59 ms | 527 / 616 Mio |

Dans ce duel strict, BpmnGo gagne clairement à 10 clients, mais devient plus
variable et parfois plus lent à 50/100 clients. Camunda a le meilleur travail
utile par CPU ; BpmnGo a de très loin la meilleure empreinte mémoire.

## 6. Niveau de performance du BpmnGo actuel

Les optimisations transactionnelles postérieures au duel donnent :

| Clients | BpmnGo actuel P1 / P2 | Moyenne | Camunda A/B P1 / P2 | Moyenne Camunda | Écart indicatif |
|---:|---:|---:|---:|---:|---:|
| 10 | 233,41 / 236,42 | 234,91 | 178,52 / 160,31 | 169,42 | +38,7 % |
| 50 | 284,53 / 276,74 | 280,63 | 241,56 / 225,46 | 233,51 | +20,2 % |
| 100 | 285,85 / 280,84 | 283,34 | 246,32 / 234,95 | 240,64 | +17,7 % |

La variance BpmnGo tombe à 1,3–2,7 % entre passages et les 96 074 instances
mesurées se terminent sans erreur. À 100 clients, p50/p95/p99 sont
328–335 / 637–648 / 771–781 ms, contre 375–391 / 774–821 / 957–1 022 ms pour
Camunda dans la baseline.

Ces résultats indiquent que le BpmnGo actuel a probablement dépassé Camunda
sur ce workflow précis et ce matériel. Une conclusion formelle exige néanmoins
un rerun A/B complet, alterné et sur volumes neufs.

## 7. Endurance, PostgreSQL et archivage

### Endurance de 30 minutes

- 419 428 instances terminées ;
- 232,98 instances/s, zéro erreur ;
- p50 396,9 ms, p95 779,9 ms, p99 1 001,6 ms ;
- 8,27 ms CPU/instance ;
- cache PostgreSQL d'environ 99,40 %, aucun deadlock ni fichier temporaire ;
- autovacuum actif et débit stable malgré la croissance des tables.

Cette endurance précédait la correction finale de l'archiveur. Elle révélait
un backlog d'environ 349 000 instances : 46 archives/s sous charge et 350/s
après charge.

### Correction actuelle de l'archiveur

La campagne ciblée actuelle mesure :

| Mesure | Avant | Après |
|---|---:|---:|
| Foreground | 289,07 inst/s | 291,71 inst/s |
| Archive sous charge | ~60/s | ~141/s |
| Rattrapage | ~370/s | ~439/s |
| WAL complet/instance | 23,89 Kio | 21,87 Kio |

29 321 archives ont été écrites en 3 155 transactions, soit 9,29 instances par
commit. L'admission foreground descend de 8 à 4 puis 2 quand le backlog dépasse
1 000 puis 5 000. Le backlog du test court a été entièrement résorbé.

La campagne longue de 30–60 minutes n'a pas encore été répétée avec cette
dernière admission/réservation CPU. La stabilité longue est probable mais pas
encore prouvée dans la configuration finale.

## 8. Architecture et exploitation

| Axe | Camunda 8.10.7 | BpmnGo actuel |
|---|---|---|
| État runtime | partitions Zeebe, stockage local répliqué | PostgreSQL ou Citus partagé |
| Ordonnancement | partitionné nativement | direct par défaut, mode Kafka partitionné optionnel |
| Plafond de scale-out | ajout de brokers/partitions | capacité PostgreSQL/Citus, même en mode partitionné |
| Haute disponibilité | architecture native Camunda | Patroni/HAProxy testé, failover 21–28 s |
| Pooling SQL | non applicable au moteur Zeebe | PgBouncer transaction, pools à dimensionner |
| Historique | composants Camunda dédiés | tables PostgreSQL mensuellement partitionnées |
| Sauvegarde | mécanismes de la plateforme Camunda | `pg_dump` et restauration familière PostgreSQL |
| Observabilité SQL | non | excellente : statements, IO, WAL, bloat, partitions |
| Complexité minimale | pile Camunda plus lourde | application + PostgreSQL, faible RAM |

L'avantage BpmnGo est opérationnel si l'équipe maîtrise déjà PostgreSQL. Son
inconvénient structurel est également PostgreSQL : moteur, audit, historique
et archivage se disputent encore la même capacité CPU/WAL. Citus distribue les
tables, mais ne transforme pas l'ensemble en architecture share-nothing
équivalente à Zeebe.

## 9. Avantages de BpmnGo

1. **Mémoire** : 0,28–0,32× Camunda dans l'A/B, avantage majeur pour petits
   clusters et déploiements edge/on-premise.
2. **Débit actuel** : 18–39 % au-dessus de la baseline Camunda sur le workflow
   synthétique, avec une variance désormais faible.
3. **Déploiement BPMN** : environ 2 à 3 fois plus rapide en latence médiane.
4. **Simplicité de données** : SQL, sauvegardes, requêtes d'exploitation,
   partitions et outils PostgreSQL standards.
5. **Double compatibilité** : API Camunda 7 partielle et API/gRPC Camunda 8
   dans un seul moteur.
6. **Fonctions propres** : audit détaillé, replay de tokens, SSE/WebSocket,
   frontend léger, précédent/move token.
7. **Coût d'infrastructure potentiel** : empreinte RAM faible et absence de
   JVM ; aucun coût financier n'a toutefois été mesuré.

## 10. Lacunes de BpmnGo

1. **Efficacité CPU** : encore environ 1,5–1,6× le CPU/instance de Camunda dans
   les comparaisons indicatives actuelles.
2. **Historique** : dernier résultat propre 46/47 ; exhaustivité et cohérence
   immédiate à revalider après archivage.
3. **Écosystème** : pas d'équivalent complet à Operate, Tasklist, Optimize,
   Connectors et Web Modeler ; le frontend BpmnGo n'en couvre qu'une tranche.
4. **Compatibilité SDK** : 19 RPCs du protocole zeebe-node 8.3, mais pas encore
   `MigrateProcessInstance` ni `UpdateJobTimeout`; Spring Zeebe et plusieurs
   SDKs récents ne sont pas qualifiés.
5. **REST `/v2`** : sous-ensemble, pas contrat exhaustif Camunda ; le mode REST
   du client Java officiel n'est pas qualifié comme remplacement transparent.
6. **User tasks** : gérées comme tâches REST natives BpmnGo, pas comme jobs
   `io.camunda.zeebe:userTask`.
7. **Sécurité transport** : le gateway gRPC n'offre pas encore TLS natif ; il
   faut terminer TLS devant le service.
8. **Scale-out** : la base partagée reste le plafond, contrairement au stockage
   par partition des brokers Zeebe.
9. **Maturité** : couverture de tests solide mais nettement inférieure aux
   années de production, intégrations et scénarios réels de Camunda.
10. **Validation finale manquante** : pas encore de rerun A/B Camunda complet ni
    d'endurance longue après les dernières optimisations d'archive.

## 11. Avantages de Camunda

- meilleure efficacité CPU par instance dans toutes les campagnes A/B ;
- architecture de partitions et réplication conçue pour le scale-out ;
- contrats clients, outils d'opération et écosystème beaucoup plus vastes ;
- meilleure option pour migration transparente d'une application Camunda 8
  complexe, usage SaaS, Connectors ou exploitation multi-équipe ;
- historique et fonctionnalités de plateforme plus complets.

## 12. Lacunes de Camunda dans ce benchmark

- consommation mémoire environ 3 à 4 fois supérieure ;
- déploiement de modèles plus lent ;
- débit inférieur au BpmnGo actuel sur le workflow synthétique observé ;
- pile plus lourde pour une petite installation self-hosted ;
- plus de composants à exploiter lorsque le besoin se limite à l'exécution
  BPMN et quelques workers.

Ces points ne mesurent ni le prix des licences/SaaS, ni la qualité du support,
ni la sécurité d'entreprise : aucune conclusion financière ne doit être tirée
de ce benchmark seul.

## 13. Choix recommandé

### Choisir BpmnGo si

- les processus utilisés sont couverts par la suite et par des tests métier ;
- PostgreSQL est une compétence centrale de l'équipe ;
- la RAM, le self-hosting simple et le contrôle du code sont prioritaires ;
- l'application utilise surtout Zeebe gRPC, les workers et le REST BpmnGo
  qualifié ;
- l'équipe accepte de qualifier chaque montée de version et les lacunes API.

### Choisir Camunda si

- la compatibilité totale avec l'écosystème Camunda est obligatoire ;
- Operate/Tasklist/Optimize/Connectors/Web Modeler sont structurants ;
- le scale-out indépendant d'une base relationnelle partagée est requis ;
- le risque produit, le support et la maturité priment sur la RAM ;
- les SDKs récents, migrations d'instances et contrats REST complets sont
  nécessaires.

## 14. Verdict

**BpmnGo actuel : 8,2/10 comme remplaçant ciblé de Camunda 8, mais 6,5/10
comme remplacement universel.**

- **Comportement** : 46/47 prouvé sur volume neuf ; très proche, pas parfait.
- **Performance** : probablement meilleur débit/latence sous charge depuis les
  optimisations, mais rerun A/B final encore nécessaire.
- **Ressources** : très supérieur en mémoire, encore inférieur en CPU.
- **Architecture** : excellent moteur PostgreSQL compact ; plafond de scale-out
  moins favorable que Zeebe.
- **Produit** : adapté à un périmètre contrôlé, pas encore à un remplacement
  transparent de toute la plateforme Camunda.

## 15. Prochaines validations prioritaires

1. Rejouer les 47 scénarios sur volumes neufs avec le BpmnGo actuel et exiger
   47/47, notamment l'historique `start/work/end`.
2. Refaire le duel complet Camunda/BpmnGo après les optimisations, deux passages
   alternés par palier.
3. Lancer 60 minutes avec la dernière admission d'archive et vérifier backlog,
   WAL, bloat, autovacuum et checkpoints.
4. Ajouter des workflows plus lourds : grandes variables, parallélisme,
   multi-instance, timers et incidents sous charge.
5. Tester les SDKs réellement utilisés en production et établir une matrice
   endpoint/RPC/version.
6. Réaliser des drills de panne comparables, des deux côtés, avec perte de nœud,
   base, réseau et reprise workers.

## Sources internes vérifiables

- `benchmark/REPORT-RERUN-20261029.md`
- `benchmark/REPORT-TRANSACTION-ROADMAP-20261029.md`
- `benchmark/REPORT-ENDURANCE-BIGINT-POSTGRESQL-20261029.md`
- `benchmark/REPORT-ARCHIVER-ADMISSION-BATCHING-20261029.md`
- `benchmark/raw/rerun-20261029/summary.json`
- `benchmark/raw/postgresql-transaction-roadmap-20261029/`
- `benchmark/raw/archiver-admission-batching-20261029/`
- `benchmark/cmd/bench/` et `benchmark/scripts/`
