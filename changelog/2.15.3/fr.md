## Corrections
- Une dépendance dont le volume de données a été supprimé (par exemple depuis la page des volumes) n'est plus proposée comme une mise à niveau dont la migration échoue ensuite : faute de données à protéger, la version du bundle s'applique au prochain démarrage.
- Quand une migration nécessite un lanceur plus récent, `citeck deps` indique désormais de relancer `install.sh` ; `citeck update` ne rafraîchit que les définitions du workspace et des bundles.
- L'avertissement du bureau avant la suppression d'un namespace précise désormais que ses volumes de données et son dossier, snapshots compris, sont également supprimés.
