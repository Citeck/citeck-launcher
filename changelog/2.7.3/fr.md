## Nouveautés
- Modifiez la configuration de l'espace de travail (workspace-v1.yml) directement depuis l'écran d'accueil — une roue dentée à côté du sélecteur d'espace de travail ouvre un éditeur YAML avec marqueurs de modifications. Vos modifications sont enregistrées comme un delta sur la référence git et réappliquées à chaque synchronisation.
- Choisissez précisément quels volumes inclure lors de la création d'un instantané.

## Corrections
- Les statuts dans l'application de bureau se mettent désormais à jour en temps réel sur toutes les plateformes ; sous Windows, ils ne semblaient plus figés (par exemple tous sur « Chargement ») pendant un démarrage long jusqu'à ce que vous appuyiez sur « Arrêter ».
- Sous Windows, la fenêtre ne devient plus impossible à cliquer après la confirmation du mot de passe maître ou la fermeture d'une boîte de dialogue imbriquée.
- Les échecs de téléchargement d'images consignent désormais la cause dans les journaux (par exemple une connexion au registre refusée), pour comprendre pourquoi un téléchargement n'a pas abouti.
- La liste des namespaces affiche des statuts localisés, et les namespaces supprimés disparaissent de la liste de lancement rapide.
- L'écran d'accueil est lisible dans le thème clair, et les cases natives s'affichent correctement et alignées dans les deux thèmes.
- L'en-tête du namespace se met à jour de façon fiable après un renommage, et choisir une valeur dans une liste déroulante ne décale plus la mise en page.
- L'installateur lance désormais l'application automatiquement après une installation ou une mise à niveau sous Windows.
