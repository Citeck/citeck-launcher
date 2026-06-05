## Corrections
- Le Démarrage rapide « avec données de démonstration » charge désormais réellement les données de démonstration ; auparavant, l'espace de noms pouvait démarrer avec des bases de données vides.
- L'espace de noms créé par le Démarrage rapide s'appelle de nouveau « Citeck Default » au lieu de reprendre le libellé du bouton.
- Le tableau de bord et la liste des espaces de noms affichent maintenant la version réelle du bundle (par exemple 2026.3-RC1) au lieu de « LATEST ».
- « Rechercher des mises à jour » n'affiche plus d'erreur 404 lorsque vous disposez déjà de la dernière version.
- Correction des erreurs d'authentification RabbitMQ après un redémarrage du serveur (« impossible de charger le menu ») : le conteneur RabbitMQ manquait de mémoire pour terminer la configuration de son compte de service et obtenait une nouvelle identité à chaque redémarrage. Il conserve désormais une identité stable et dispose de suffisamment de mémoire.
- L'archive de diagnostic (dump-system-info) masque désormais les mots de passe, les jetons et autres secrets dans les variables d'environnement des conteneurs, les journaux et les fichiers de configuration.
