## Corrections
- Le service IA (enregistrement des appels / reconnaissance vocale) est désormais accessible via le proxy, et la liaison avec le sidecar STT est appliquée correctement.
- L'attachement ou le détachement de l'IA, du sidecar STT ou d'OnlyOffice à l'exécution prend désormais effet immédiatement — le proxy et les liens IA sont mis à jour sans recréer le namespace.
- Les microservices externes exécutés hors de Docker peuvent de nouveau atteindre le serveur de configuration cloud du bureau (pour obtenir les adresses RabbitMQ, ZooKeeper et base de données) ; il n'écoutait auparavant, par erreur, que sur la boucle locale.
- L'invite « saisir les identifiants du registre » réapparaît désormais de façon fiable lorsqu'un téléchargement d'image échoue avec une erreur d'authentification, même si la notification unique a été manquée.
- La suppression d'un identifiant de registre ne laisse plus de liaison orpheline « (introuvable) » pour son hôte.
- La fenêtre de l'éditeur de configuration de l'espace de travail est plus grande et plus lisible, et « Réinitialiser vers git » s'applique désormais en un clic.
- Les cellules CPU et mémoire ne conservent plus une surbrillance bloquée après une sélection accidentelle.
- L'installateur Windows ne se bloque plus sur « Annulation… » après une installation ou une mise à jour, ce qui pouvait bloquer d'autres installateurs.
