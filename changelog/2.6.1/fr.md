## Nouveautés
- Zoomez l'interface avec les contrôles de zoom natifs du webview.
- Redémarrez une seule application depuis un menu contextuel, disponible dans n'importe quel état de l'application.

## Corrections
- L'installation serveur avec données de démonstration importe désormais réellement le snapshot de démonstration : auparavant le namespace était créé mais démarrait avec des données vides.
- L'installation serveur enregistre désormais l'identifiant de registre privé saisi pendant la configuration, au lieu de l'abandonner silencieusement.
- Meilleure parité avec le launcher 1.x pour les configurations migrées ou modifiées à la main : utilisateurs de namespace par défaut (admin + fet), réglages bundle/workspace et valeurs par défaut probe/log-startup.
- Vous êtes désormais invité à définir un mot de passe maître avant d'ajouter votre premier secret, au lieu d'obtenir une erreur.
- Quick Start fixe la version la plus récente à une release concrète, actualise les versions disponibles après le déverrouillage des secrets et ouvre le panneau du namespace dès le démarrage.
- Les dépôts de workspace personnalisés se rétablissent automatiquement après l'ajout ou la correction de leur jeton d'accès — aucun redémarrage nécessaire.
- Les identifiants de registre enregistrés sont immédiatement liés à leur hôte de registre.
- Les valeurs des secrets sont clairement étiquetées Token ou Password.
- Boîte de dialogue des snapshots : un seul en-tête partagé et une actualisation automatique à la fin d'un export ou d'un import.
- Panneau de détails de l'application plus contrasté ; l'éditeur de configuration suit désormais le thème de l'application.
- Texte des journaux plus lisible, et une indication expliquant quand « Tout supprimer » est désactivé.
- La suppression d'un workspace supprime désormais aussi ses liaisons d'identifiants de registre.
