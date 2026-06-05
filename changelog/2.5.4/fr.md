## Corrections
- Les espaces de noms migrés depuis la version 1.x ne sont plus corrompus au premier démarrage : une réimportation automatique de cliché non souhaitée, qui pouvait endommager la base de données, a été supprimée. L'importation d'un cliché est désormais toujours une action manuelle.
- Après le changement d'espace de noms actif, l'application affiche les conteneurs de cet espace de noms — l'en-tête et les détails de l'application ne se contredisent plus.
- La suppression d'un espace de noms supprime désormais aussi ses volumes, son réseau et ses conteneurs Docker, afin que les données résiduelles ne s'accumulent plus.
- L'utilisation du processeur par les conteneurs s'affiche de nouveau correctement (elle restait bloquée à 0 %).
- La visionneuse de journaux ne se fige plus après le redémarrage d'un conteneur — elle se reconnecte automatiquement.
- Le formulaire de création/modification d'espace de noms est plus clair : les dépôts de bundles sans versions publiées sont masqués, et la dernière version est sélectionnée automatiquement lors du changement de dépôt.
- Le titre de la fenêtre de bureau reflète désormais la version en cours d'exécution.
- Les journaux du démon sont conservés dans un seul dossier « logs » (comme dans la version 1.x) au lieu d'être répartis après une mise à jour.
