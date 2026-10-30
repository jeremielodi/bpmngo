Tu vas comparer deux moteurs de workflow BPMN de façon indépendante et équitable : **Camunda 8** (la référence) et **BpmnGo** (un moteur écrit en Go qui se veut compatible avec Camunda 8). Une autre IA fait la même comparaison de son côté ; vos deux rapports seront comparés. Travaille sans chercher à favoriser l'un ou l'autre moteur : un résultat défavorable à BpmnGo est aussi utile qu'un résultat favorable.

## Versions à utiliser

- **Camunda** 8.10.7, avec la distribution Docker Compose officielle :
  https://github.com/camunda/camunda-distributions/releases/download/docker-compose-8.10/docker-compose-8.10.zip (fichier `docker-compose.yaml`, sans modification sauf les limites ci-dessous).
- **BpmnGo** : https://github.com/jeremielodi/bpmngo, commit `22d88a7` (Release 1.0.1), avec son `docker-compose.yaml`. Utilise sa configuration par défaut (mode d'écriture `direct`, sans Kafka/Redpanda). Note dans ton rapport si tu changes un réglage.
- Les deux moteurs tournent sous Docker sur **la même machine**. BpmnGo doit être déplacé sur d'autres ports (par exemple 9080 pour le REST et 26600 pour le gRPC) car les deux utilisent 8080 et 26500 par défaut.

## Règles d'équité (obligatoires)

1. **Même capacité** : limite chaque moteur à **2 cœurs dédiés et 2 Go de mémoire au total** (avec `cpuset` et `mem_limit` dans un fichier d'override Docker Compose). Pour BpmnGo, ce budget est partagé par tous ses conteneurs (application + Postgres). Vérifie les limites avec `docker inspect` avant chaque mesure.
2. **Rien d'autre ne tourne** sur la machine pendant les mesures (pas d'autre pile Docker active, pas de compilation). Vérifie-le avec `docker stats`.
3. **Même code client** pour les deux moteurs : passe uniquement par les API publiques que Camunda 8 expose — le **gRPC Zeebe** (proto officiel `gateway.proto`) et le **REST `/v2`**. Une seule différence permise : les identifiants (Camunda de la distribution n'en demande pas ; BpmnGo utilise `admin@goflow.com` / `admin123` en HTTP Basic).
4. **Chauffe** : fais tourner chaque moteur quelques minutes avant de mesurer (la JVM de Camunda doit être chaude).
5. **Répète** chaque mesure de performance au moins **2 fois** et donne les deux valeurs ; signale tout écart de plus de 20 % entre deux passages.
6. N'invente aucun chiffre. Si une mesure échoue ou n'a pas pu être faite, écris-le.

## Ce qu'il faut tester

### A. Conformité de comportement (Camunda = référence)

Écris tes propres scénarios BPMN (au moins 40), déployés et joués à l'identique sur les deux moteurs, et compare ce qu'un client observe : jobs reçus (élément, type, variables, ordre), réponse de chaque appel (code gRPC), variables finales, état final de l'instance, incidents. Couvre au minimum :

- gateways exclusive, inclusive, parallèle (flux par défaut, aucune condition vraie, deux conditions vraies, condition sur une variable absente) ;
- événements de bord : timer (interrompant ou non), message, signal, erreur (code précis et catch-all), escalade ;
- jobs : échec avec et sans tentatives restantes, backoff, variables envoyées avec `FailJob` et `ThrowError`, en-têtes, mappings d'entrée/sortie ;
- sous-processus d'événement, multi-instance (parallèle, séquentiel, collection de sortie, condition de complétion, collection vide), call activities ;
- messages (clé de corrélation, TTL, `messageId` en double, démarrage par message, gateway événementielle), signaux, timers (durée, date passée, cycle, durée calculée en FEEL) ;
- expressions FEEL (nombres, `null`, chaînes, listes, dates et durées, contextes) ;
- tâche utilisateur Camunda complétée par le REST `/v2`, compensation ;
- API : une clé renvoyée par gRPC est-elle utilisable et renvoyée telle quelle en REST ? Recherche des éléments et variables d'une instance terminée.

Vérifie d'abord que chaque modèle se déploie sur Camunda : un modèle refusé par Camunda est un bug de ton test, pas du moteur.

### B. Ressources sur la suite de comportement

Pour chaque moteur, pendant toute sa suite : temps CPU consommé par ses conteneurs (compteur cgroup `/sys/fs/cgroup/cpu.stat`, lu avant et après), CPU moyen et pic, mémoire au repos et au pic (`docker stats`).

### C. Latence par appel

Médiane (p50) et p95 sur au moins 30 appels séquentiels : `Topology`, `DeployResource`, `CreateProcessInstance`, `CreateProcessInstanceWithResult`, délai entre création et réception d'un job par un worker, `CompleteJob`, bout en bout pour une instance à une tâche de service.

### D. Charge

Processus : début → 2 tâches de service → fin, avec quelques variables métier. Paliers de **10, 50 et 100 clients** simultanés ; chaque client démarre une instance avec `CreateProcessInstanceWithResult`, attend la fin, recommence. Mêmes workers pour les deux moteurs (par exemple 8 par type de job, lots de 32). Par palier, après 10 s de chauffe, mesure pendant 60 s : instances terminées par seconde, latence p50/p95/p99, erreurs par code gRPC (`RESOURCE_EXHAUSTED` = contre-pression), CPU moyen, CPU par instance, pic mémoire, et la répartition du CPU entre les conteneurs de BpmnGo.

## Format du rapport (à respecter exactement, pour pouvoir comparer)

1. **Environnement** : machine (CPU, RAM, OS), version de Docker, versions exactes des moteurs, limites vérifiées, réglages modifiés.
2. **Conformité** : nombre de scénarios, combien identiques ; puis un tableau de chaque écart : scénario | Camunda | BpmnGo | gravité (bloquant / moyen / mineur / libellé seulement).
3. **Ressources (suite de comportement)** : tableau moteur | durée | temps CPU | CPU moyen | CPU pic | mémoire repos | mémoire pic, avec les rapports BpmnGo/Camunda.
4. **Latence** : tableau appel | Camunda p50/p95 | BpmnGo p50/p95.
5. **Charge** : tableau clients | moteur | inst/s | p50 | p95 | p99 | erreurs | CPU moyen | CPU/instance | mémoire pic, pour chaque passage.
6. **Limites de ton test** : ce que tu n'as pas pu mesurer ou qui peut biaiser les chiffres.
7. **Verdict** : une note sur 10 pour BpmnGo comme remplaçant de Camunda 8, justifiée en 5 lignes maximum, en séparant comportement, performance et ressources. Dis clairement sur quels points BpmnGo est meilleur, égal ou moins bon que Camunda, chiffres à l'appui.

Joins à ton rapport le code de tes tests et les sorties brutes, pour que les deux comparaisons puissent être vérifiées.
