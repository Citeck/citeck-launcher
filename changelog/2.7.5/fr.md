## Corrections
- L'arrêt d'un namespace supprime désormais proprement tous ses conteneurs — une application lente à s'arrêter (par exemple eapps) ne reste plus à l'état « Exited » après l'arrêt, et le réseau du namespace est supprimé de façon fiable.
- La suppression d'un espace de travail récupère désormais tout ce qu'il contient — les volumes de données Docker de chaque namespace (PostgreSQL, MongoDB, …) et les enregistrements en base, pas seulement les fichiers sur le disque.
- Le serveur de configuration intégré (bureau) ne s'exécute désormais que tant qu'un namespace est actif et libère son port à l'arrêt, de sorte que le redémarrage de l'application avec un namespace arrêté n'échoue plus avec « address already in use ».
