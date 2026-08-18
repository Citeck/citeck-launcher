## Nouveautés
- **La mise à jour automatique fonctionne désormais sous macOS et Windows**, et plus seulement sous Linux. Le launcher télécharge lui-même la nouvelle version, vérifie sa signature et l'installe.

## Corrections
- **Une mise à jour interrompue n'empêche plus le launcher de démarrer.** Une mise à jour téléchargée mais jamais vérifiée — parce que l'application a été fermée ou s'est arrêtée entre-temps — est désormais contrôlée au démarrage suivant et annulée automatiquement si elle ne démarre pas.
- **Alfresco et Solr ne reçoivent plus d'options JVM que leur Java 8 ne comprend pas.** Une option inconnue n'est pas ignorée : la JVM refuse purement et simplement de démarrer.
- **Alfresco et Solr dimensionnent leur mémoire à partir de la limite du conteneur.** Le heap de Solr valait exactement la taille de son propre conteneur, ce qui finit par un arrêt forcé par le noyau ; Alfresco n'avait aucune limite. La limite de Solr est maintenant de 2560 Mo et celle d'Alfresco de 8 Go.
- **Les services ne réservent plus tout leur heap au démarrage.** `heapSize` ne définit que le maximum : démarrer un grand espace de noms prend la mémoire au fur et à mesure.

## Améliorations
- Le journal du démon ne répète plus le calcul mémoire de la JVM pour chaque service à chaque rechargement.
- Compilé avec Go 1.26.6, qui apporte des correctifs de sécurité pour la bibliothèque standard.

## Mise à jour
- Au premier démarrage après la mise à jour, les conteneurs Java sont recréés une fois.
