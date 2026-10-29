# Benchmark Camunda 8.10.7 vs BpmnGo 1.0.1

## 1. Environnement

- Machine : Apple M4 Max, 16 cœurs, 64 Gio de RAM ; macOS 26.6.2 (25G83), arm64.
- Docker Desktop : client/serveur 29.7.2, Compose v5.5.1 ; 7,75 Gio alloués à la VM Docker.
- Moteurs : `camunda/camunda:8.10.7` avec `camunda/connectors-bundle:8.10.2`, distribution Compose officielle 8.10 ; BpmnGo commit `22d88a729d5b71084081978b8336dcdb146d3a28` (Release 1.0.1), Postgres 16, mode `direct`.
- Capacité vérifiée avec `docker inspect` avant les mesures : CPU `0-1` pour tous les conteneurs ; Camunda orchestration 1 536 Mio + connectors 512 Mio ; BpmnGo application 1 536 Mio + Postgres 512 Mio. Le plafond est donc de 2 CPU et 2 Gio par moteur.
- Modifications : uniquement les overrides de limites ; ports BpmnGo REST/gRPC déplacés vers 9080/26600 ; authentification Basic BpmnGo `admin@goflow.com`/`admin123`. Aucun changement fonctionnel et pas de Kafka/Redpanda.
- Les sept autres conteneurs présents ont été arrêtés pendant toutes les mesures, puis restaurés. Un seul moteur tournait à la fois. Les deux moteurs ont été chauffés avant mesure ; chaque palier de charge ajoute 10 s de chauffe.

Les preuves d'environnement et de limites sont dans [`raw/environment.txt`](raw/environment.txt), les fichiers `*-hostconfig.json` de [`raw/`](raw/), et les overrides [`bpmngo.override.yaml`](bpmngo.override.yaml) / [`camunda-8.10.7/docker-compose.override.yaml`](camunda-8.10.7/docker-compose.override.yaml).

## 2. Conformité

Les 47 modèles se déploient et s'exécutent sans erreur de test sur Camunda. En comparaison stricte des effets et des réponses des API publiques, **35 scénarios sur 47 sont identiques** après neutralisation des identifiants et horodatages générés. Les 12 scénarios non identiques sont ci-dessous. Hors forme des réponses REST, 45/47 ont le même effet métier lors du passage mesuré ; les exceptions sont le choix de branche exclusive et le démarrage par message.

| Scénario | Camunda | BpmnGo | Gravité |
|---|---|---|---|
| `exclusive-two-true-reference-order` | Prend le premier flux défini par le modèle, branche `b` | Prend `a` | moyen |
| `boundary-message` | Effet correct ; réponse REST `messageKey`, `tenantId=<default>` | Effet correct ; schéma différent (`key`, listes de corrélation, tenant vide) | mineur |
| `boundary-signal-non-interrupting` | Effet correct ; réponse `signalKey`, `tenantId` | Effet correct ; réponse propriétaire `resumed/errors/signalName` | mineur |
| `job-fail-no-retries-incident` | Incident complet avec pagination, clés d'élément/définition/racine et tenant | Incident créé, mais réponse `/v2/incidents/search` réduite, sans pagination ni plusieurs champs Camunda | moyen |
| `event-subprocess-message` | Effet correct ; réponse de publication Camunda | Effet correct ; réponse de publication au schéma différent | mineur |
| `message-correlation-key` | Corrélation correcte ; réponse de publication Camunda | Corrélation correcte ; réponse de publication au schéma différent | mineur |
| `message-start-event` | Démarre une instance et livre le job | Passage de chauffe réussi, mais passage mesuré : HTTP 200, aucune instance démarrée et aucun job ; réponse REST également différente | bloquant |
| `event-based-gateway-message` | Branche message correcte ; réponse de publication Camunda | Même branche ; réponse de publication au schéma différent | mineur |
| `signal-intermediate-catch` | Signal reçu ; réponse `signalKey`, `tenantId` | Signal reçu ; réponse propriétaire `resumed/errors/signalName` | mineur |
| `user-task-rest-completion` | Recherche complète ; completion HTTP 204 sans corps | Recherche avec champs absents ; completion HTTP 200 avec message JSON | moyen |
| `grpc-key-usable-in-rest` | La clé gRPC entière est acceptée et renvoyée identiquement par REST | La clé gRPC est acceptée, mais REST renvoie une UUID différente | moyen |
| `finished-instance-elements-and-variables` | Éléments/variables terminés avec schéma, clés, métadonnées et pagination Camunda | Données métier présentes, mais schéma réduit, UUID et valeurs JSON typées différemment | moyen |

Les autres couvertures comprennent gateways, événements de bord, échec/backoff/variables de jobs, mappings et headers, sous-processus d'événement, multi-instance, call activity, TTL/dédoublonnage de messages, timers/cycles, FEEL, compensation et recherches historiques. Résultats bruts : [`raw/camunda/behavior/result.json`](raw/camunda/behavior/result.json) et [`raw/bpmngo/behavior/result.json`](raw/bpmngo/behavior/result.json).

## 3. Ressources (suite de comportement)

Le CPU moyen est calculé exactement à partir de `cpu.stat`; le pic et la mémoire viennent de `docker stats`. Un CPU de 200 % correspond aux deux cœurs alloués.

| Moteur | Durée | Temps CPU | CPU moyen | CPU pic | Mémoire repos | Mémoire pic |
|---|---:|---:|---:|---:|---:|---:|
| Camunda | 25 s | 6,63 s | 26,5 % | 55,8 % | 1 227,5 Mio | 1 262,8 Mio |
| BpmnGo | 23 s | 18,27 s | 79,4 % | 101,0 % | 432,8 Mio | 435,3 Mio |
| Rapport BpmnGo/Camunda | 0,92× | 2,76× | 3,00× | 1,81× | 0,35× | 0,34× |

BpmnGo termine légèrement plus vite et consomme environ 65 % moins de mémoire, mais utilise 2,76 fois plus de temps CPU sur cette suite. Sur BpmnGo, les compteurs donnent 7,67 s CPU pour l'application et 10,60 s pour Postgres.

## 4. Latence

Chaque cellule donne passage 1 / passage 2, en millisecondes, avec 30 appels séquentiels par passage et zéro erreur.

| Appel | Camunda p50 / p95 | BpmnGo p50 / p95 |
|---|---:|---:|
| `Topology` | 0,492/1,408 · 0,315/0,614 | 0,461/1,123 · 0,330/0,423 |
| `DeployResource` | 4,722/9,253 · 2,766/7,007 | 1,876/2,574 · 1,489/2,171 |
| `CreateProcessInstance` | 2,104/4,930 · 2,005/2,878 | 2,471/3,169 · 2,358/2,833 |
| `CreateProcessInstanceWithResult` (bout en bout, processus sans tâche) | 2,134/4,773 · 2,002/6,959 | 2,553/2,924 · 2,446/2,826 |
| Création → réception du job | 3,374/8,463 · 3,394/6,743 | 3,434/3,775 · 3,374/4,022 |
| `CompleteJob` | 2,204/3,915 · 1,945/2,736 | 2,193/2,717 · 2,040/2,594 |

Écarts inter-passage supérieurs à 20 % : Camunda sur `Topology` p50/p95, `DeployResource` p50/p95, et les p95 de `CreateProcessInstance`, `CreateProcessInstanceWithResult`, réception du job et `CompleteJob`; BpmnGo sur `Topology` p50/p95 et `DeployResource` p50. Les deux passages sont conservés plutôt que moyennés.

## 5. Charge

Processus identique début → deux tâches de service → fin, 8 workers par type, lots de 32, boucle fermée `CreateProcessInstanceWithResult`, 10 s de chauffe puis 60 s mesurées. Latences en ms, mémoire en Mio. `CPU moy.` inclut tous les conteneurs ; pour BpmnGo, `(app/PG)` donne sa répartition. `CPU/inst.` est estimé à partir du CPU moyen échantillonné.

| Clients | Moteur/passage | inst/s | p50 | p95 | p99 | Erreurs | CPU moy. | CPU/inst. | Mémoire pic |
|---:|---|---:|---:|---:|---:|---|---:|---:|---:|
| 10 | Camunda P1 | 173,07 | 53,64 | 101,60 | 126,91 | 0 | 97,3 % | 5,62 ms | 1 535 Mio |
| 10 | Camunda P2 | 168,55 | 52,93 | 102,93 | 135,31 | 0 | 94,8 % | 5,62 ms | 1 478 Mio |
| 10 | BpmnGo P1 | 215,08 | 44,05 | 69,81 | 86,40 | 0 | 178,5 % (66,5/112,0) | 8,30 ms | 464 Mio |
| 10 | BpmnGo P2 | 192,07 | 46,89 | 79,93 | 129,43 | 0 | 171,8 % (62,0/109,8) | 8,94 ms | 498 Mio |
| 50 | Camunda P1 | 231,08 | 190,57 | 441,90 | 575,53 | 0 | 98,0 % | 4,24 ms | 1 935 Mio |
| 50 | Camunda P2 | 194,07 | 199,43 | 503,77 | 899,70 | 0 | 81,1 % | 4,18 ms | 1 875 Mio |
| 50 | BpmnGo P1 | 242,43 | 189,19 | 388,93 | 498,26 | 0 | 180,3 % (66,9/113,5) | 7,44 ms | 550 Mio |
| 50 | BpmnGo P2 | 201,29 | 204,19 | 473,35 | 1 345,49 | 0 | 167,3 % (63,5/103,8) | 8,31 ms | 556 Mio |
| 100 | Camunda P1 | 214,19 | 407,63 | 974,14 | 1 456,10 | 0 | 90,5 % | 4,22 ms | 1 932 Mio |
| 100 | Camunda P2 | 178,81 | 417,00 | 1 271,50 | 3 741,05 | 2 309 `RESOURCE_EXHAUSTED` | 79,2 % | 4,43 ms | 1 788 Mio |
| 100 | BpmnGo P1 | 225,41 | 418,37 | 798,99 | 972,33 | 0 | 187,8 % (66,2/121,6) | 8,33 ms | 494 Mio |
| 100 | BpmnGo P2 | 192,50 | 451,25 | 997,54 | 1 818,09 | 0 | 171,6 % (59,0/112,7) | 8,91 ms | 553 Mio |

BpmnGo fournit 4 à 24 % plus d'instances/s selon le palier et le passage, sans erreur ; Camunda applique de la contre-pression au second passage à 100 clients. BpmnGo dépense toutefois environ 1,7 à 2,1 fois plus de CPU par instance et sature presque les deux cœurs. Postgres représente 62 à 65 % du CPU combiné de BpmnGo.

Écarts inter-passage supérieurs à 20 % : débit BpmnGo à 50 clients (20,4 % en prenant P1 comme base) ; p99 Camunda à 50 et 100 clients, p95 Camunda à 100 ; p99 BpmnGo aux trois paliers. Cela signale surtout une forte variabilité de queue, pas des chiffres à lisser.

## 6. Limites de ton test

- Le passage `CreateProcessInstanceWithResult` mesure un processus début → fin sans tâche ; le bout en bout à une tâche de service n'a pas été isolé en série de 30. La charge mesure bien le bout en bout à deux tâches.
- `docker stats --no-stream` sur Docker Desktop est lent : 5 à 6 échantillons sur la suite de comportement et 14 à 15 par palier. Le CPU total est donc plus fiable via les compteurs cgroup ; pics CPU/mémoire ont une résolution grossière.
- Les deux répétitions exposent une variance de queue importante ; deux passages ne suffisent pas à produire un intervalle de confiance.
- La pile Camunda officielle inclut `connectors`, même s'il est presque inactif ici. BpmnGo inclut obligatoirement Postgres ; c'est équitable au niveau du système utilisable, mais les architectures sont différentes.
- Le démarrage par message BpmnGo a fonctionné au passage de chauffe puis échoué au passage mesuré. Le test démontre une non-reproductibilité, sans isoler si la cause est une course d'abonnement après déploiement.

## 7. Verdict

**6,5/10** comme remplaçant de Camunda 8 dans le périmètre testé.
Comportement : 35/47 strictement identiques, 45/47 à effet métier identique ; démarrage par message instable et contrat REST incomplet empêchent un remplacement transparent.
Performance : BpmnGo est meilleur en débit (+4 à +24 %) et souvent en p95/p99, sans contre-pression observée, mais ses queues sont variables.
Ressources : BpmnGo est nettement meilleur en mémoire (environ 0,34×), mais moins bon en CPU par instance (environ 1,7 à 2,1×).
Il est crédible pour des workflows contrôlés après tests de compatibilité, pas encore égal à Camunda comme substitut API général.

## Code et sorties brutes

- Harness Go et génération BPMN : [`cmd/bench/`](cmd/bench/)
- Scripts de mesure/reproductibilité : [`scripts/`](scripts/)
- Résumé machine : [`raw/summary.json`](raw/summary.json)
- Toutes les observations, compteurs cgroup, échantillons Docker et résultats des deux passages : [`raw/`](raw/)
