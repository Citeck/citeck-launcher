## Corrections
- **La migration depuis la 1.x reprend désormais tous vos workspaces et namespaces.** Une seule entrée par page de stockage était lue : une installation comportant 19 namespaces n'en migrait qu'un — tout en signalant une migration réussie. Les workspaces, les namespaces, les dépôts git, l'état des applications arrêtées et les fichiers modifiés des namespaces sont maintenant restaurés. Cela corrige la migration elle-même ; les données perdues lors d'une mise à jour déjà effectuée ne sont pas récupérées automatiquement — votre base 1.x d'origine est intacte dans `storage.db.kotlin-bak`, contactez-nous pour la réimporter.
- **macOS : la mise à jour ne fige plus le lanceur.** Après un clic sur « Mettre à jour », la fenêtre restait sur « Mise à jour… » avec tous les boutons désactivés, y compris « Annuler », et le lanceur était inutilisable jusqu'à son redémarrage. La fenêtre se recharge maintenant d'elle-même dès que la nouvelle version tourne, « Annuler » reste toujours actif, et les boutons ne se décalent plus au lancement de la mise à jour.

## Mises à jour
- Mailpit 1.31.0, pgAdmin 9.17 et ZooKeeper 3.9.5.
