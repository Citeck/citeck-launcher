## Nouveautés

- Paramètres de services compagnons pour les applications web (`webapps[].companions`) : un workspace peut fournir des variables d’environnement et des dépendances de démarrage à une application web uniquement lorsque le service compagnon (par exemple une passerelle LLM optionnelle) est présent sur l’environnement, tout en conservant la priorité des surcharges du namespace.
- Bases de données dans le PostgreSQL principal pour les services additionnels (`additionalApps[].postgresDatabases`) : les services personnalisés peuvent demander des bases dans le cluster PostgreSQL principal de l’environnement afin qu’elles soient incluses dans les migrations, les snapshots et pgAdmin sans recréer PostgreSQL lors de l’activation ou de la désactivation de modules.
- Info-bulles localisées pour les liens personnalisés de la barre latérale (`links[].descriptions`) : les liens du workspace prennent désormais en charge des descriptions par langue avec repli rétrocompatible, et l’interface les rafraîchit immédiatement lors du changement de langue.
- Ajout de l’icône LiteLLM dans la barre latérale et mise à jour de l’icône Mailpit.
