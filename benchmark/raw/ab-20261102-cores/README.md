# A/B BpmnGo / Camunda 8.10.7 — 2 novembre 2026

Machine : Intel i7-10750H (portable), Docker Desktop sous WSL2. Valeurs
absolues environ 10× plus lentes que les rapports du 29 octobre (Mac M4
Max) : seuls les rapports entre les deux moteurs sont comparables.

Placement CPU : les overrides du dépôt réservent les CPU 0-1, qui sur ce
processeur sont un seul cœur physique (hyperthreads). Cette campagne utilise
deux cœurs physiques distincts : BpmnGo `app` sur le CPU 2, `postgres` sur
2,4 ; Camunda `orchestration` et `connectors` sur 2,4.

**`bpmngo/load-*-pass1` est invalide** : une campagne précédente, non arrêtée,
tournait en parallèle (avec son propre Camunda) pendant ce passage. Utiliser
`pass2` pour BpmnGo ; les deux passages Camunda sont propres.

Résultats : `bpmngo-behavior.json` / `camunda-behavior.json` sont identiques
après normalisation (47/47). Synthèse de charge dans `summary.json` (CPU par
instance = delta `usage_usec` des cgroups des conteneurs du moteur divisé par
les instances terminées).
