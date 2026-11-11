# Camunda 8.10.7 vs BpmnGo 1.0.1

Dernière exécution depuis des images fraîchement téléchargées et des bases neuves :
[`raw/fresh-20261109T140844Z/REPORT.md`](raw/fresh-20261109T140844Z/REPORT.md).
Les 142 anciens conteneurs ont été supprimés à la demande de l'utilisateur ;
les volumes ont été conservés. Ce rapport consigne aussi une limite du test
répété de démarrage par message, qui réutilise une instance encore active.

Les chemins de l'hôte sont normalisés dans les captures de configuration.
L'inventaire des conteneurs sans rapport avec le benchmark est exclu des
fichiers et archives publiés. Les mesures et résultats sont conservés.

Ce dossier autonome contient le rapport demandé, le client Go commun aux deux
moteurs, les scripts de mesure et les sorties brutes. La campagne définitive a
été exécutée le 9 novembre 2026 avec BpmnGo
`22d88a729d5b71084081978b8336dcdb146d3a28` et Camunda 8.10.7. Le client utilise
les bindings de `camunda-community-hub/zeebe-client-go` générés depuis le
protocole Zeebe officiel. Toutes les données définitives sont dans
`raw/official-20261109/`.

- Rapport : [`REPORT.md`](REPORT.md)
- Client : [`cmd/bench/`](cmd/bench/)
- Scripts : [`scripts/`](scripts/)
- Sorties brutes : [`raw/`](raw/)
- Override BpmnGo 1.0.1 : [`bpmngo.override.yaml`](bpmngo.override.yaml)
- Override Camunda 8.10.7 : [`camunda.override.yaml`](camunda.override.yaml)

Le client mesure aussi `EndToEndOneServiceTask`, bout en bout sur un processus
à une tâche de service. Pour le rebâtir :

```sh
go build -o bench ./cmd/bench
```
