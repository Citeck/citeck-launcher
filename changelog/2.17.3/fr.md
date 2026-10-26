## Corrections
- L’import et l’export d’instantanés affichent leur progression et retrouvent le résultat après une coupure de connexion, sans rester bloqués sur le chargement.
- La migration PostgreSQL après restauration d’un instantané reconnaît les anciens répertoires de données et télécharge automatiquement l’image source manquante.
- Le déverrouillage des secrets actualise les namespaces arrêtés : S3 et les applications utilisant des secrets deviennent disponibles sans nouveau redémarrage.
- Les bannières de démarrage restent masquées jusqu’à la fin de la saisie du mot de passe maître et de l’actualisation qui suit.
- Les migrations et retours arrière de dépendances terminés ne laissent plus la dernière étape tourner ni les commandes du namespace bloquées.
- Les événements de l’application de bureau conservent leur ordre ; la synchronisation normale ne génère plus de notifications répétées « Connexion rétablie ».
- Les outils d’administration d’Alfresco Share fonctionnent à nouveau dans les environnements avec Alfresco.
- La vérification HTTPS rejette les certificats intermédiaires compromis connus.
