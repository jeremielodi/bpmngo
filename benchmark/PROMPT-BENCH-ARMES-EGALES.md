Tu vas exécuter, sur cette machine, le benchmark **BpmnGo ⇄ Camunda 8** fourni dans le dépôt https://github.com/jeremielodi/bpmngo (dossier `benchmark/`), dans des conditions **strictement égales pour les deux moteurs**. Le but est de reproduire de façon indépendante les chiffres publiés dans `benchmark/README.md`, pas de les confirmer : un résultat défavorable à BpmnGo est aussi utile qu'un résultat favorable. Ne favorise aucun moteur, n'invente aucun chiffre, et écris noir sur blanc tout ce qui a échoué ou que tu n'as pas pu faire.

Lis d'abord `benchmark/README.md` en entier : c'est la documentation de référence de l'outil. Ce prompt ne fait qu'imposer les conditions d'équité et le format du rapport.

## 0. Règles

1. **Ne modifie ni le code du bench, ni les scénarios, ni `known-differences.json`, ni les fichiers `limits/`.** Si quelque chose ne marche pas sans modification, arrête-toi, explique le problème et la modification minimale que tu proposes, et attends l'accord de la personne.
2. **Même version pour tous** : clone le dépôt et place-toi sur le commit `034d33a` (`git checkout 034d33a`). Note `git rev-parse HEAD` dans le rapport.
3. **Rien d'autre ne tourne** pendant les mesures : aucune autre pile Docker (`docker ps` ne doit montrer que les conteneurs listés plus bas), pas de compilation, pas de navigateur lourd, machine sur secteur (pas sur batterie, pas en mode économie d'énergie).
4. **Ne supprime aucun volume Docker qui n'appartient pas à ce benchmark.** Avant tout `docker compose down -v`, vérifie avec `docker compose ls` / `docker volume ls` que tu vises bien le projet BpmnGo ou le projet Camunda de ce dépôt, et demande confirmation si la machine a déjà d'autres projets.
5. Chaque mesure de performance est faite **au moins 3 fois** (`--repeat 3`) ; signale tout écart de plus de 20 % entre passages.
6. **Contre-pression et critère de débit identiques** : laisse la contre-pression de chaque moteur à son réglage par défaut (Camunda : AIMD de la distribution ; BpmnGo : `BACKPRESSURE` actif, `BACKPRESSURE_MAX_WAIT_MS=150`). Ne la désactive ni ne la règle d'un côté seulement. Le seuil « débit tenu » reste celui du bench pour les deux : aucun refus, ≥ 98 % d'instances terminées, p99 ≤ 1000 ms (`--p99-limit 1000`, ne le change pas).

## 1. Prérequis et inventaire de la machine

- Docker (Docker Desktop ou moteur Linux), Node.js 20+, git.
- Idéalement **au moins 6 cœurs physiques** (2 pour Camunda, 2 pour BpmnGo, le reste pour le client du bench) et 8 Go de RAM. Avec moins, fais quand même le test mais signale-le comme limite majeure.
- Relève : modèle de CPU, nombre de cœurs physiques/logiques, RAM, OS, version de Docker (`docker version`), ressources allouées à la VM Docker si Docker Desktop (CPU/RAM), Node (`node -v`).
- **Topologie des cœurs** :
  ```shell
  docker run --rm alpine cat /sys/devices/system/cpu/cpu*/topology/thread_siblings_list
  ```
  Choisis deux cœurs **physiques distincts** pour chaque moteur, sans partage d'hyperthread entre les deux moteurs (ex. si `0,1` / `2,3` / … sont des paires : Camunda `2,4`, BpmnGo `6,8`). Exporte-les et garde-les pour toute la session :
  ```shell
  export BENCH_CAMUNDA_CPUS=2,4
  export BENCH_BPMNGO_CPUS=6,8
  ```
- **Windows / WSL** : `localhost` peut tomber sur le relais WSL au lieu de Docker. Utilise **`127.0.0.1`** partout (options `--camunda-rest http://127.0.0.1:8080 --camunda-grpc 127.0.0.1:26500 --bpmngo-rest http://127.0.0.1:9080 --bpmngo-grpc 127.0.0.1:26600`).

## 2. Démarrer Camunda 8.10.7 (2 cœurs, 2 Go, 2 partitions)

Depuis `benchmark/camunda-8.10.7` :

```shell
printf 'CAMUNDA_VERSION=8.10.7\nCAMUNDA_CONNECTORS_VERSION=8.10.2\n' > .env
docker compose -f docker-compose.yaml -f ../limits/camunda.yaml -f ../limits/camunda-2p.yaml up -d
docker stop connectors      # seconde JVM, inutilisée par le bench
```

Attends que `orchestration` soit healthy, puis vérifie :
- `curl http://127.0.0.1:8080/v2/topology` → `partitionsCount` = **2** ;
- `docker inspect orchestration --format '{{.HostConfig.CpusetCpus}} {{.HostConfig.Memory}}'` → tes cœurs Camunda et `2147483648`.

Garde cette même configuration Camunda pour **toutes** les mesures (compare, latency, load).

## 3. Démarrer BpmnGo (mode direct par défaut, 2 cœurs, 2 Go au total)

Depuis la racine du dépôt, **sur des volumes neufs** (la taille de l'historique en base change le débit d'environ 25 %) :

```shell
docker compose down -v      # uniquement le projet BpmnGo de ce dépôt, voir règle 4
SERVER_PORT=9080 GRPC_PORT=26600 docker compose -f docker-compose.yaml -f benchmark/limits/bpmngo-direct.yaml up -d postgres bpmngo
```

Configuration par défaut, ne change rien d'autre (`WRITE_PATH=direct`, `FEEL_MODE=shadow`, `EXCLUSIVE_GATEWAY_ORDER=camunda`, `REST_KEYS=int`, pas de Redpanda). Vérifie, et colle les sorties dans le rapport :
- `docker inspect bpmngo-app bpmngo-postgres --format '{{.Name}} {{.HostConfig.CpusetCpus}} {{.HostConfig.Memory}}'` → tes cœurs BpmnGo et `1073741824` chacun ;
- `docker exec bpmngo-postgres psql -U goflow -d goflow -tc "show timezone"` → `UTC` ;
- `docker exec bpmngo-postgres psql -U goflow -d goflow -tc "select max(version) from schema_migrations"` → `023` (une migration manquante fait tourner l'archiveur en boucle et fausse le CPU) ;
- **CPU au repos** : `docker stats --no-stream` à 3 reprises sur une minute, moteur inactif → BpmnGo doit être proche de 0 %. Sinon, arrête-toi et regarde `docker logs bpmngo-app`.

## 4. Mesures (depuis `benchmark/`)

```shell
npm install
```

Ajoute à **chaque** commande les options `127.0.0.1` du §1 si tu es sous Windows, et `--bpmngo-containers bpmngo-app,bpmngo-postgres` (Redpanda ne tourne pas en mode direct).

1. **Conformité + ressources** (≈ 15 min) :
   `npm run compare -- --out results/ext-compare`
2. **Latence par appel** :
   `npm run latency -- --iterations 30 --repeat 2 --out results/ext-latency`
3. **Charge** — remets d'abord BpmnGo **et** Camunda sur des volumes neufs (§2 avec `down -v` dans `camunda-8.10.7`, §3), refais les vérifications, puis :
   `npm run load -- --levels 10,50,100,200 --rates 5,10,15,20,25,30,40,50,60,70 --repeat 3 --out results/ext-load`
   (≈ 2 h). Le balayage des débits doit **dépasser le plafond de chaque moteur** : s'il s'arrête avant, les deux moteurs paraissent égaux à la dernière valeur. Si un moteur tient encore le dernier débit, ajoute des paliers plus hauts jusqu'à ce qu'il ne tienne plus.
   - **Affine le plafond** de chaque moteur, sur volumes neufs : par pas de 2 inst/s entre son dernier débit tenu et le premier non tenu, par exemple `npm run load -- --engines camunda --rates 20,22,24 --repeat 3 --out results/ext-knee-camunda` (et la même chose pour `bpmngo`).
   - **Vérifie que le client n'est pas le goulot** : aucun `CLIENT_LIMIT` dans les erreurs, et le CPU du processus Node du bench reste sous 100 % d'un cœur. En mode clients, le débit doit plafonner entre 100 et 200 clients. S'il monte encore, c'est le nombre de clients qui limitait, pas le moteur.
   - **Débit tenu dans la durée** : sur volumes neufs, relance chaque moteur seul au débit maximal tenu qu'il a trouvé, pendant 10 minutes (`npm run load -- --engines bpmngo --rates <N> --duration 600 --out results/ext-endurance-bpmngo`, même chose pour `camunda`). Si le p99 dérive ou que des refus apparaissent, ce débit n'est pas tenable et doit être donné comme tel. Pour BpmnGo, regarde aussi le retard de l'archiveur : `docker exec bpmngo-postgres psql -U goflow -d goflow -tc "select count(*) from process_instances where status <> 'running'"` (instances terminées pas encore archivées) au début et à la fin. Ce nombre doit rester stable, pas grossir.
4. **Charge sur un modèle réaliste** (volumes neufs à nouveau) :
   `npm run load -- --process bpmn/large.bpmn --levels 10,50 --repeat 3 --out results/ext-load-large`
5. *(optionnel)* **Camunda réglé** — pour répondre à « Camunda était mal configuré » : redémarre `orchestration` en ajoutant `-f ../limits/camunda-tuned.yaml`, puis refais le 3 avec `--engines camunda --out results/ext-load-camunda-tuned`.
6. *(optionnel)* **FEEL natif de BpmnGo** : redémarre BpmnGo avec `FEEL_MODE=feel`, puis `npm run compare -- --engines bpmngo --reference results/ext-compare/traces/camunda --out results/ext-compare-feel`.

Entre deux mesures, vérifie que la précédente est bien terminée et que les deux moteurs sont revenus au repos (`docker stats --no-stream`).

## 5. Rapport (format à respecter, pour pouvoir le comparer aux autres)

1. **Environnement** : machine, OS, Docker, Node, commit, cœurs attribués à chaque moteur et au bench, sorties `docker inspect`, `partitionsCount`, fuseau et migration Postgres, CPU au repos. Tout écart à ce prompt.
2. **Conformité** : N scénarios, combien SAME / EXPECTED / DIFFERENT / MISSING ; tableau des écarts : scénario | Camunda | BpmnGo | gravité (bloquant / moyen / mineur / libellé seulement) | ton analyse (bug du moteur ou du harnais ?).
3. **Ressources sur la suite** : la section « Resources (Docker) » de `report.txt`, avec les rapports BpmnGo/Camunda.
4. **Latence** : appel | Camunda p50/p95 | BpmnGo p50/p95.
5. **Charge** : reprends `load-report.txt` (paliers clients et débits fixes, moyenne ± écart-type et chaque passage), le CPU par instance, et pour BpmnGo les chiffres PostgreSQL par instance. Ensuite, un tableau des plafonds : moteur | débit max tenu sur 60 s (affiné) | tenu 10 min ? | premier débit non tenu et pourquoi (refusé = contre-pression ; mis en file = p99 > 1000 ms ; `CLIENT_LIMIT` = limite du bench) | débit plafond en mode clients et à combien de clients.
6. **Comparaison avec les chiffres publiés** dans `benchmark/README.md` : ce qui se reproduit, ce qui ne se reproduit pas, de combien.
7. **Limites de ton test** : tout ce qui peut biaiser (cœurs partagés, Docker Desktop, machine portable, thermique, etc.).
8. **Verdict** : en 5 lignes maximum, séparant comportement, performance et ressources, chiffres à l'appui.

Joins une archive de tous les dossiers `results/ext-*` (rapports, `*.json`, traces) : c'est ce qui permet de vérifier tes chiffres.
