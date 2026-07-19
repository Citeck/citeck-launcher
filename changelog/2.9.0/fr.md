## Nouveautés
- **HTTPS pour un espace de noms (auto-signé).** Une nouvelle option « Activer HTTPS (auto-signé) » dans la boîte de dialogue de l'espace de noms sert le proxy en HTTPS avec un certificat généré automatiquement. Le port suit automatiquement le schéma — 443 pour HTTPS, 80 pour HTTP — de sorte qu'activer et désactiver HTTPS fonctionne sans effort. Le navigateur affiche un avertissement unique pour le certificat auto-signé.

## Changements
- **Les modifications de configuration sont chirurgicales et instantanées.** Modifier les paramètres d'un espace de noms ou d'une application ne recrée désormais que les conteneurs dont la configuration a réellement changé, et reflète le changement immédiatement — les images des services non concernés ne sont plus retéléchargées. Pour récupérer les dernières images, utilisez « Mettre à jour et démarrer ».
- **Le démarrage attend le mot de passe maître.** Lorsqu'un espace de noms télécharge depuis un registre privé et que vos secrets sont verrouillés, il attend désormais que vous saisissiez le mot de passe maître avant de démarrer — ainsi la boîte de dialogue des identifiants ne masque plus l'écran de déverrouillage.
