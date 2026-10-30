# Admission dynamique et transactions d'archive groupées

Date : 2026-10-29.

## Changements vérifiés

- admission foreground dynamique selon le backlog : limite 8 sous le seuil,
  4 à partir de 1 000 instances et 2 à partir de 5 000 ;
- CPU 1 réservé à PostgreSQL (`bpmngo=0`, `postgres=0-1`) dans le profil de
  benchmark, toujours dans l'enveloppe totale de deux CPU ;
- jusqu'à 10 instances archivées dans une même transaction SQL ;
- suppression des `VARIABLES_MERGED` qui dupliquaient exactement
  `JOB_COMPLETED`/`TASK_COMPLETED`, et retrait des variables du payload
  `EXECUTION_MOVED` ;
- `track_wal_io_timing=on` dans les profils PostgreSQL standalone, scale et
  Citus.

## Protocole

- BpmnGo limité au CPU 0 et à 1 280 Mio ; PostgreSQL aux CPU 0-1 et à
  768 Mio ; tous les autres conteneurs arrêtés ;
- même processus `début → deux service tasks → fin`, 100 clients, 8 workers,
  activation par lots de 32 ;
- chauffe de 10 s puis 90 s mesurées ;
- statistiques PostgreSQL remises à zéro avant la campagne ;
- mesure poursuivie jusqu'au backlog zéro.

Les données brutes sont sous
`benchmark/raw/archiver-admission-batching-20261029/`.

## Résultats

| Mesure | Avant | Après | Évolution |
|---|---:|---:|---:|
| Débit foreground mesuré | 289,07 inst/s | 291,71 inst/s | +0,91 % |
| Archive sous charge | ~60 inst/s | ~141 inst/s | +136 % |
| Archive après arrêt de charge | ~370 inst/s | ~439 inst/s | +18,5 % |
| WAL complet par instance | 23,89 Kio | 21,87 Kio | -8,45 % |
| Erreurs de charge | 0 | 0 | stable |

La campagne a mesuré 26 311 instances et en a créé 29 314 chauffe comprise.
Les sept instances qui restaient dans le volume avant le reset expliquent les
29 321 archives observées.

### Admission

Les métriques ont montré la transition attendue :

| Backlog observé | Limite foreground |
|---:|---:|
| < 1 000 | 8 |
| 1 689 | 4 |
| 5 680 puis 9 374 et 13 883 | 2 |

Entre les échantillons à 5 680 et 13 883 éléments en attente, l'archiveur a
traité environ 141 instances/s tout en maintenant le débit foreground global.

### Transactions d'archive

Les métriques finales donnent 29 321 instances archivées en 3 155 transactions,
soit 9,29 instances par commit. Par rapport à une transaction par instance,
cela évite 26 166 paires `BEGIN`/`COMMIT`. Le backlog de 13 055 instances
mesuré au début du rattrapage est tombé à 1 065 en 27,34 s, puis à zéro.

### Audit et WAL

Les appels SQL d'insertion audit sont regroupés par l'écrivain asynchrone :
2 319 appels ont produit 160,9 Mo de WAL, contre 208,9 Mo dans la campagne
précédente. La famille archive/historique est passée de 226,9 à 207,1 Mo malgré
un nombre d'instances légèrement supérieur.

La validation statique et les tests garantissent que seuls les doublons
identifiés ont été retirés : les sources `signal`, `gatewayJoin` et les autres
mises à jour autonomes conservent `VARIABLES_MERGED`, tandis que
`EXECUTION_MOVED` conserve l'identifiant et les nœuds source/cible sans recopier
les variables.

`pg_stat_wal` rapporte 1 312,6 ms d'écriture WAL et 31 945,7 ms de
synchronisation. La mesure est donc désormais active et montre que le temps de
sync WAL reste un axe distinct de la contention CPU.

## Conclusion

La limitation initiale est confirmée et corrigée dans la direction attendue :
l'archiveur n'avait pas besoin de workers supplémentaires. La réservation CPU,
le ralentissement contrôlé du foreground et les commits groupés multiplient par
environ 2,36 son débit sous charge, sans régression du débit foreground, tout en
réduisant le WAL complet de 8,45 % par instance.
