# Rerun du benchmark Camunda 8.10.7 vs BpmnGo amélioré — 29 octobre 2026

## 1. Environnement

- Machine : Apple M4 Max, 16 cœurs, 64 Gio de RAM ; macOS 26.6.2 (25G83), arm64.
- Docker Desktop : client/serveur 29.7.2, Compose v5.5.1 ; 7,75 Gio alloués à la VM Docker.
- Camunda : `camunda/camunda:8.10.7` et `camunda/connectors-bundle:8.10.2`, distribution Compose officielle 8.10.
- BpmnGo : base commit `22d88a729d5b71084081978b8336dcdb146d3a28` (1.0.1), avec les corrections de compatibilité documentées dans [`IMPROVEMENTS.md`](IMPROVEMENTS.md), Postgres 16, écriture `direct`.
- Limites vérifiées avant mesure : CPU `0-1`; orchestration/application 1 536 Mio et Connectors/Postgres 512 Mio, soit 2 CPU et 2 Gio par moteur.
- BpmnGo : REST/gRPC 9080/26600, PostgreSQL hôte 15432, authentification Basic `admin@goflow.com`/`admin123`, réponses `/v2` en clés int64 compatibles Camunda.
- Bases et volumes neufs pour ce rerun. Un seul moteur tournait pendant les mesures ; les sept autres conteneurs ont été arrêtés puis restaurés. Chaque moteur a subi deux minutes de chauffe, plus 10 s avant chaque palier de charge.

## 2. Conformité

Les deux moteurs ont déployé les 47 modèles. Camunda termine 47/47 sans erreur. BpmnGo produit des observations normalisées identiques dans **46/47 scénarios**. La seule différence a été reproduite lors d'un second passage BpmnGo sur la même base.

| Scénario | Camunda | BpmnGo | Gravité |
|---|---|---|---|
| `finished-instance-elements-and-variables` | Recherche d'une instance terminée : `start`, `work`, `end` et deux variables | Variables correctes, mais l'historique des éléments ne renvoie que `end`; `start` et `work` manquent, deux passages sur deux | moyen |

Les corrections précédentes sont confirmées : message start reproductible, ordre de gateway correct, mêmes clés gRPC/REST, HTTP 204 pour les tâches utilisateur, et schémas compatibles pour messages, signaux, incidents, instances, éléments et variables. La comparaison neutralise seulement identifiants, dates, versions de déploiement, curseurs opaques et durée murale du backoff.

Résultats : [`camunda/behavior/result.json`](raw/rerun-20261029/camunda/behavior/result.json), [`bpmngo/behavior/result.json`](raw/rerun-20261029/bpmngo/behavior/result.json) et [`bpmngo/behavior-repeat.json`](raw/rerun-20261029/bpmngo/behavior-repeat.json).

## 3. Ressources (suite de comportement)

| Moteur | Durée | Temps CPU | CPU moyen | CPU pic | Mémoire repos | Mémoire pic |
|---|---:|---:|---:|---:|---:|---:|
| Camunda | 26 s | 7,48 s | 28,8 % | 77,8 % | 1 929 Mio | 1 929 Mio |
| BpmnGo | 21 s | 21,53 s | 102,5 % | 107,6 % | 528 Mio | 528 Mio |
| Rapport BpmnGo/Camunda | 0,81× | 2,88× | 3,56× | 1,38× | 0,27× | 0,27× |

BpmnGo termine la suite 19 % plus vite et utilise 73 % moins de mémoire, mais consomme 2,88 fois plus de temps CPU. Sur ses 21,53 s CPU, l'application représente 8,86 s et PostgreSQL 12,66 s.

## 4. Latence

Chaque cellule donne passage 1 / passage 2 en millisecondes, 30 appels séquentiels par passage, zéro erreur.

| Appel | Camunda p50 / p95 | BpmnGo p50 / p95 |
|---|---:|---:|
| `Topology` | 0,374/1,365 · 0,287/0,511 | 0,355/0,522 · 0,355/0,534 |
| `DeployResource` | 5,587/18,516 · 4,562/7,576 | 1,794/4,397 · 1,685/2,224 |
| `CreateProcessInstance` | 2,181/4,952 · 1,851/3,909 | 3,261/4,051 · 3,887/5,783 |
| `CreateProcessInstanceWithResult` | 2,027/3,319 · 1,677/3,470 | 3,452/4,724 · 3,428/5,591 |
| Création → réception du job | 4,479/11,618 · 2,716/7,639 | 4,895/6,290 · 5,853/7,670 |
| `CompleteJob` | 1,867/4,430 · 1,835/3,050 | 2,201/3,957 · 2,262/3,126 |

BpmnGo est nettement plus rapide au déploiement. Camunda garde de meilleures médianes de création et `CreateWithResult`; la réception de job BpmnGo a une queue p95 meilleure ou équivalente. Écarts inter-passage supérieurs à 20 % : Camunda sur plusieurs p95 et sur `Topology`/réception de job ; BpmnGo sur les p95 de création, déploiement, réception de job et completion.

## 5. Charge

Processus début → deux tâches de service → fin, 8 workers par type, lots de 32, boucle fermée, 10 s de chauffe puis 60 s mesurées. Latences en ms et mémoire en Mio. Pour BpmnGo, `(app/PG)` indique la répartition du CPU moyen.

| Clients | Moteur/passage | inst/s | p50 | p95 | p99 | Erreurs | CPU moy. | CPU/inst. | Mémoire pic |
|---:|---|---:|---:|---:|---:|---|---:|---:|---:|
| 10 | Camunda P1 | 178,52 | 51,87 | 96,96 | 124,15 | 0 | 95,9 % | 5,37 ms | 1 892 Mio |
| 10 | Camunda P2 | 160,31 | 56,67 | 110,99 | 147,94 | 0 | 89,8 % | 5,60 ms | 1 925 Mio |
| 10 | BpmnGo P1 | 218,20 | 43,03 | 69,66 | 85,67 | 0 | 184,9 % (64,8/120,1) | 8,48 ms | 533 Mio |
| 10 | BpmnGo P2 | 198,99 | 44,99 | 77,20 | 134,78 | 0 | 174,9 % (59,3/115,7) | 8,79 ms | 530 Mio |
| 50 | Camunda P1 | 241,56 | 178,46 | 423,81 | 568,85 | 0 | 106,7 % | 4,42 ms | 1 876 Mio |
| 50 | Camunda P2 | 225,46 | 193,46 | 464,09 | 618,79 | 0 | 92,7 % | 4,11 ms | 1 889 Mio |
| 50 | BpmnGo P1 | 237,35 | 191,23 | 407,25 | 526,23 | 0 | 183,7 % (66,9/116,8) | 7,74 ms | 542 Mio |
| 50 | BpmnGo P2 | 198,41 | 205,18 | 474,63 | 936,17 | 0 | 168,1 % (60,1/108,0) | 8,47 ms | 528 Mio |
| 100 | Camunda P1 | 246,32 | 374,53 | 774,01 | 956,97 | 0 | 106,7 % | 4,33 ms | 1 777 Mio |
| 100 | Camunda P2 | 234,95 | 390,75 | 820,82 | 1 022,45 | 0 | 96,8 % | 4,12 ms | 1 902 Mio |
| 100 | BpmnGo P1 | 247,69 | 378,71 | 724,52 | 881,15 | 0 | 183,1 % (65,7/117,5) | 7,39 ms | 527 Mio |
| 100 | BpmnGo P2 | 196,84 | 455,97 | 1 026,98 | 1 380,00 | 0 | 169,1 % (56,8/112,3) | 8,59 ms | 616 Mio |

À 10 clients, BpmnGo fournit 22 à 24 % plus de débit. À 50 clients, il varie de −2 à −12 % face à Camunda ; à 100 clients, de +0,6 à −16 %. Aucun moteur n'a produit d'erreur gRPC dans ce rerun. BpmnGo utilise 1,6 à 2,1 fois plus de CPU par instance, tandis que sa mémoire de pointe vaut seulement 28 à 32 % de celle de Camunda. PostgreSQL représente environ 64 à 66 % du CPU BpmnGo.

Écarts inter-passage supérieurs à 20 % : débit BpmnGo à 100 clients (−20,5 % en prenant P1 comme base) ; p99 BpmnGo aux trois paliers et p95 BpmnGo à 100 clients. La variabilité de queue reste donc le principal risque de performance.

## 6. Limites de ton test

- `CreateProcessInstanceWithResult` séquentiel utilise un processus début → fin sans tâche ; la charge mesure le bout en bout à deux tâches, mais le cas isolé à une tâche n'est toujours pas séparé.
- `docker stats --no-stream` ne fournit que 5–6 échantillons sur la suite de comportement et 14–16 par palier. Le CPU total cgroup est précis, les pics ont une résolution plus grossière.
- Deux passages révèlent la variance mais ne constituent pas un intervalle de confiance statistique.
- Les modifications BpmnGo sont présentes dans le working tree au-dessus de `22d88a7`; ce rerun ne représente donc pas la release 1.0.1 publiée sans corrections.
- Le second passage de conformité BpmnGo a été exécuté après restauration des autres conteneurs, uniquement pour confirmer le défaut historique ; aucune mesure de performance n'en dépend.

## 7. Verdict

**8/10** comme remplaçant de Camunda 8 dans le périmètre testé.
Comportement : 46/47 observations identiques ; le contrat API est désormais proche, mais l'historique immédiat des éléments terminés reste incomplet.
Performance : BpmnGo est meilleur à faible concurrence, comparable au meilleur passage à 100 clients, mais moins stable en p99 et parfois moins rapide à 50/100 clients.
Ressources : BpmnGo est très supérieur en mémoire (0,28–0,32×), mais inférieur en efficacité CPU (1,6–2,1× par instance).
Il devient crédible comme substitut contrôlé, sous réserve de corriger l'historique et de profiler PostgreSQL.

## Sorties vérifiables

- Résumé consolidé : [`raw/rerun-20261029/summary.json`](raw/rerun-20261029/summary.json)
- Toutes les sorties, compteurs et échantillons : [`raw/rerun-20261029/`](raw/rerun-20261029/)
- Harness : [`cmd/bench/`](cmd/bench/)
- Scripts : [`scripts/`](scripts/)
