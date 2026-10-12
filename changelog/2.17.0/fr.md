## Nouveautés
- À la création d’un espace de noms, choisissez DB ou S3 ; la disponibilité de S3 dépend du bundle et de l’édition sélectionnés.
- L’API pour agents et les commandes de découverte sans démarrage du daemon décrivent les opérations prises en charge par Launcher.
- La récupération Git propose le cache, une nouvelle tentative et un choix de jeton par dépôt, avec la date de dernière synchronisation réussie.
- Les téléchargements de mises à jour et d’instantanés affichent les octets reçus et reprennent automatiquement après une interruption.

## Corrections
- Les téléchargements actifs continuent tant que des octets arrivent, sans limite de durée totale.
- L’initialisation normale de S3 n’affiche plus de bannière ; les erreurs de configuration et de connexion restent visibles.
- Mailpit utilise les ports hôte 17011 pour SMTP et 17012 pour son interface web.
- Les diagnostics Git masquent les identifiants intégrés aux URL des dépôts.
