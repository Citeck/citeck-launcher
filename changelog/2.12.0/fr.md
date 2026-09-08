## Nouveautés
- **Les versions de l'infrastructure sont désormais liées aux données.** Une mise à jour du bundle ne peut plus faire passer PostgreSQL, RabbitMQ, ZooKeeper, Keycloak ou MongoDB à une version incompatible avec les données déjà présentes dans un namespace. Le launcher conserve la version avec laquelle les données ont été créées et signale la nouvelle — via une bannière et le nouveau dialogue **Dépendances** de l'application de bureau, et via `citeck deps` sur un serveur.
- **Migration de PostgreSQL 17 vers 18**, depuis le dialogue « Dépendances » ou avec `citeck deps upgrade postgres`. Le namespace est arrêté, les données sont exportées, un nouveau cluster est créé dans un **nouveau** volume, restauré et vérifié, puis le namespace bascule dessus — avec la progression de chaque étape. Toute erreur est automatiquement annulée et les anciennes données sont conservées.
- L'ancien volume de données **est conservé** après une migration réussie (il figure sur la page « Volumes ») ; supprimez-le vous-même une fois le résultat validé.
- Si un volume cible subsiste d'une tentative précédente, le launcher affiche sa taille et sa version et ne le supprime qu'après votre confirmation.

## Modifications
- Les nouveaux namespaces sont créés avec PostgreSQL 18 ; les namespaces existants restent en PostgreSQL 17 jusqu'à leur migration.
- Les mises à jour de RabbitMQ, ZooKeeper, Keycloak et MongoDB sont signalées mais pas encore appliquées : elles attendent une version du launcher capable de les migrer.
- Modifier l'image d'une dépendance vers une version incompatible avec ses données est refusé, dans l'éditeur de configuration comme dans `citeck edit`.
- Le démarrage, le rechargement et la suppression d'un namespace sont refusés pendant un instantané ou une migration de dépendance ; le message précise laquelle.
