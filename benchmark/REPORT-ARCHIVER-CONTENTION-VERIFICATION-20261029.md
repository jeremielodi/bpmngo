# Vérification de la contention de l'archiveur

Date : 2026-10-29.

## Protocole

- BpmnGo et PostgreSQL seuls, épinglés sur les CPU `0-1`.
- Budget mémoire total : 2 Gio (application 1 280 Mio, PostgreSQL 768 Mio).
- Processus `début → deux service tasks → fin`, 100 clients, 8 workers,
  lots de 32.
- Chauffe de 10 s puis charge mesurée pendant 90 s.
- Échantillonnage toutes les 10 s de `process_instances`, des insertions
  `historic_process_instances` et de `pg_stat_wal`.
- Attente du rattrapage complet après l'arrêt de la charge.

## Résultat

La charge a terminé 26 078 instances mesurées à 289,07 inst/s, sans erreur.
Chauffe comprise, 28 869 instances ont été créées puis archivées.

| Phase | Débit de l'archiveur | Observation |
|---|---:|---|
| Charge active | environ 60 inst/s | 6 100 archives en 101,3 s ; backlog final proche de 22 400 |
| Rattrapage | environ 370 inst/s | 18 632 archives en 50,3 s entre le premier échantillon post-charge et backlog zéro |

L'ancienne mesure de 46 inst/s sous charge et 350 inst/s après charge est
donc confirmée dans sa conclusion, avec des valeurs actuelles améliorées :
environ +31 % sous charge et +6 % au rattrapage. L'archiveur ne suit toujours
qu'environ 21 % du débit de création lorsque les deux CPU sont saturés.

## CPU ou I/O ?

PostgreSQL a servi 41,49 millions de blocs depuis le cache contre seulement
93 891 lectures, soit environ 99,77 % de cache hits. Les temps cumulés de
lecture et écriture de blocs sont de 1,55 s, contre 459,9 s d'activité backend
cumulée. La limitation observée est donc principalement la concurrence CPU,
verrous/transactions et génération de WAL, pas l'attente de lecture disque.
`track_wal_io_timing` n'étant pas activé, cette campagne ne sépare pas le coût
CPU de construction du WAL de son temps de synchronisation.

Les lots adaptatifs sont déjà actifs (10 à 100 instances) et l'archiveur est
déjà sérialisé. Ajouter des workers sans borne n'est donc pas recommandé :
cela augmenterait la concurrence sur PostgreSQL. Pour maintenir l'archiveur à
niveau sous charge, il faut soit réserver davantage de CPU à PostgreSQL, soit
réduire temporairement l'admission du chemin foreground quand le backlog
dépasse un seuil.

## WAL

- À la fin de la charge, avant rattrapage complet : environ 549,2 Mo, soit
  19,0 kB (18,6 Kio) par instance créée.
- Après archivage complet : 706 278 432 octets, soit 24,46 kB
  (23,89 Kio) par instance sur son cycle complet.

Le chiffre historique de 20,2 « Kio » avait été capturé avant que l'archiveur
ait rattrapé la majorité du backlog. La comparaison équivalente est donc
19,0 kB aujourd'hui contre 20,2 kB auparavant, environ 6 % de moins. Le coût
complet, archivage inclus, est logiquement plus élevé.

Répartition des 706,3 Mo :

| Famille | WAL | Part |
|---|---:|---:|
| Archive/historique | 226,9 Mo | 32,1 % |
| Audit batch | 208,9 Mo | 29,6 % |
| Jobs | 87,3 Mo | 12,4 % |
| Exécutions | 62,7 Mo | 8,9 % |
| Instances | 41,1 Mo | 5,8 % |
| Variables | 39,2 Mo | 5,5 % |
| Autres | 37,4 Mo | 5,3 % |

Audit et archivage représentent ensemble 61,7 % du WAL complet. Agrandir les
lots réduit les transactions et les commits, mais pas le volume des lignes et
index écrits. Les prochains gains WAL doivent donc venir de la suppression
d'événements d'audit réellement redondants, de payloads plus petits ou d'une
projection historique moins répétitive, avec validation fonctionnelle.

Les statistiques PostgreSQL brutes sont dans
`benchmark/raw/archiver-contention-verification-20261029/after/`.

La mise en œuvre et la campagne après optimisation sont documentées dans
[`REPORT-ARCHIVER-ADMISSION-BATCHING-20261029.md`](REPORT-ARCHIVER-ADMISSION-BATCHING-20261029.md).
