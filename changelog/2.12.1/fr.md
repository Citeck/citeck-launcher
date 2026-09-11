## Nouveautés
- **Un bundle ou une configuration d'espace de travail peut déclarer les images des composants tiers dans une nouvelle section `dependencies:`.** Les lanceurs antérieurs à 2.12 ne lisent pas cette section : une montée de version de PostgreSQL, de RabbitMQ ou d'une autre brique d'infrastructure n'atteint donc que les lanceurs capables de protéger les données existantes — les autres continuent de fonctionner tels quels. Dans la configuration d'espace de travail, la section relève aussi la version pour tous les namespaces de cet espace à la fois.

## Corrections
- **Une mise à jour du bureau n'est plus annulée simplement parce que Docker était lent.** Le démon répond désormais dès que son processus est vivant, et non à la toute fin de son démarrage : le contrôle de santé juge donc la nouvelle version, et non la durée de démarrage de la machine. Signalé sous Windows : une version parfaitement saine a été installée, déclarée « ne démarre pas » après 60 secondes d'attente d'un Docker Desktop bloqué, puis annulée.
- **Une mise à jour ayant échoué peut être réinstallée.** Une version ayant échoué une fois était jusqu'ici refusée pour toujours ; la fenêtre de mise à jour propose maintenant **Réessayer**, le lanceur ne la réessayant toujours jamais de lui-même.
- La fenêtre de mise à jour n'affirme plus que vous êtes sur la dernière version à côté d'un échec, et ne propose plus de redémarrer pour « terminer » une mise à jour annulée.
- `citeck reload` sur un namespace arrêté n'attend plus indéfiniment : la configuration est rechargée et la commande indique qu'elle s'appliquera au prochain démarrage. Aucune attente d'un namespace ou du démon ne peut plus rester bloquée sans fin.

## Changements
- pgAdmin choisit son image comme toutes les autres applications tierces. Sur un poste de bureau où la configuration d'espace de travail et le bundle nomment tous deux une image pgAdmin, c'est celle du bundle qui l'emporte et le conteneur est recréé une fois.
- Une mise à jour échouée indique désormais sa cause dans le journal, le démon note au démarrage la version qu'il exécute, et le dump système contient le journal du lanceur lui-même ainsi que le manifeste des mises à jour.
