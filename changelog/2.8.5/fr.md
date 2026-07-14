## Changements
- La liste des applications est plus épurée : la colonne « Ports » et la ligne d'en-têtes de colonnes ont été supprimées.
- Les en-têtes « Applications » et « Ressources » de la barre latérale sont désormais traduits dans toutes les langues.

## Sécurité
- Mode serveur : l'interface web intégrée du launcher ne peut plus être exposée via TCP. Elle n'a jamais été une interface serveur prise en charge — c'est la CLI/TUI qui l'est — et elle est désormais désactivée dans le code, et plus seulement par défaut.
