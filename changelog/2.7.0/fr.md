## Nouveautés
- Modifiez la configuration d'une application et ses fichiers montés directement — même lorsque le namespace est arrêté. Vos modifications sont enregistrées comme des deltas par-dessus la configuration générée et réappliquées à chaque régénération, de sorte que les nouvelles versions d'image et de bundle continuent de passer tout en conservant vos changements.
- Une gouttière de modifications dans l'éditeur marque les lignes modifiées et ajoutées ; cliquez sur un marqueur pour rétablir la ligne à la valeur générée.
- Détails de l'image : empreinte (sha256), taille et plateforme depuis le panneau de l'application, plus le téléchargement explicite d'une image — même un tag de version. Les applications utilisant l'image se mettent à jour automatiquement après le téléchargement.
- Utilisation mémoire actuelle et limite de chaque application dans le panneau de détails.
- Téléchargez les journaux d'une application ou du démon directement dans votre dossier Téléchargements, avec une notification et un bouton « Ouvrir le dossier ».

## Améliorations
- Protection renforcée de votre mot de passe maître (dérivation de clé Argon2id) ; les secrets existants continuent de fonctionner.
- « Mettre à jour et démarrer » actualise désormais les dépôts de bundles avant le démarrage ; le sélecteur de bundle liste tous les dépôts configurés et permet d'actualiser celui sélectionné.
- Boîtes de dialogue des secrets plus claires : pas de « Ignorer » quand un mot de passe maître est défini, la boîte de migration propose une réinitialisation, et la création d'un mot de passe a un bouton « Annuler ».
- Journaux plus lisibles : couleurs de niveau adaptées au thème clair et messages d'erreur de configuration en chasse fixe.

## Corrections
- Une application détachée ne reste plus bloquée sur « En file d'attente » après une mise à jour forcée.
- Un redémarrage manuel ou l'application d'une configuration n'augmente plus le compteur de redémarrages ; la valeur du panneau correspond au badge du tableau.
- L'éditeur ne clignote plus avec un fond noir ou blanc à l'ouverture.
- Menus contextuels : le premier clic qui ferme un menu ne sélectionne plus d'application et ne décale plus la ligne, et ouvrir un menu ne modifie plus la hauteur de la ligne.
- Le bouton Arrêter est affiché pendant la mise à jour d'une application et désactivé pendant son arrêt ; l'en-tête du tableau des applications reste fixé lors du défilement.
- Sur le bureau, fermer l'application arrête désormais de manière fiable le démon en arrière-plan sur toutes les plateformes.
