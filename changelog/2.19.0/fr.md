## Nouveautés

- Connectez des agents IA locaux via MCP pour consulter l’état et les journaux ; le rôle Developer permet aussi de gérer les applications, de modifier les paramètres d’exécution et la configuration Spring, et de recueillir des diagnostics JVM.
- Gérez l’accès des agents dans les paramètres de l’application de bureau : approuvez les connexions OAuth ou créez des jetons manuellement, choisissez Observer ou Developer, définissez leur expiration et révoquez l’accès. MCP est désactivé par défaut et, une fois activé, n’écoute que sur l’interface de bouclage.
- Définissez les limites CPU de chaque application en nombre de cœurs, y compris des valeurs fractionnaires, dans l’éditeur de configuration.

## Corrections

- Le rechargement signale désormais les échecs d’écriture des fichiers de configuration générés au lieu de continuer avec des fichiers non enregistrés.
- Les fichiers de configuration modifiés ne sont plus remplacés silencieusement par un contenu vide si le fichier actuel est indisponible et que les modifications enregistrées ne peuvent pas être réappliquées.
- Les modifications de configuration YAML conservent tous les documents des fichiers contenant plusieurs documents YAML.
- Les commandes de diagnostic JVM ne sont plus répétées automatiquement si elles ont peut-être déjà été transmises à la JVM.
