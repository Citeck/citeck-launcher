## Nouveautés
- Le lanceur vous avertit lorsque le répertoire de données Docker manque d'espace disque, avant que les conteneurs ne commencent à échouer.

## Corrections
- « Forcer la mise à jour et démarrer » répond désormais instantanément et force une vérification des nouvelles versions de bundles via Git, tout en réutilisant les images de version déjà téléchargées au lieu de les retélécharger.
- Les applications bloquées lors de l'arrêt se rétablissent désormais d'elles-mêmes et ne figent plus l'espace de noms.
- Le rechargement d'un espace de noms ne reste plus bloqué indéfiniment si une opération Git se fige.
- Les redémarrages automatiques de conteneurs réutilisent l'image locale, de sorte qu'ils ne changent jamais de version en silence ni n'échouent en cas d'indisponibilité du registre.
- Les conteneurs, volumes et réseaux Docker laissés par des espaces de noms supprimés sont désormais nettoyés automatiquement au démarrage.
- L'espace de noms actif affiche toujours ses propres conteneurs, sans confusion entre espaces de noms.
- Les fenêtres supplémentaires (journaux, éditeur) se ferment désormais lorsque vous réduisez l'application dans la barre d'état système ou revenez à l'écran d'accueil.

## Modifications
- Les espaces de noms se modifient via le formulaire ; l'éditeur YAML brut et le menu contextuel de l'engrenage ont été supprimés.
