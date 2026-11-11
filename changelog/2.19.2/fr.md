## Améliorations

- Les agents MCP disposant du rôle développeur peuvent désormais modifier toute la configuration du conteneur d’une application, notamment les images, commandes, montages, sondes, dépendances et ports publics, avec les mêmes validations que dans l’interface.
- L’édition de fichiers via MCP couvre désormais les fichiers texte montés de l’application, y compris les scripts, la configuration du proxy et les fichiers dans des sous-dossiers, au lieu de se limiter au YAML Spring.

## Corrections

- Correction d’un plantage du serveur HTTP/1 causé par une condition de concurrence lors du traitement de requêtes trop volumineuses (Go 1.27.4, CVE-2026-97054).
