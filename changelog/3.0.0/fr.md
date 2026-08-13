## Nouveautés
- **Diagnostic JVM sans JDK dans l'image.** `citeck jstack <app>` pour un dump de threads, `citeck jmap <app>` pour un dump mémoire téléchargé sur votre machine, `citeck jcmd <app> <commande>` pour tout ce que la JVM elle-même sait rapporter. Cela fonctionne pendant qu'un service démarre ou tombe en panne, pas seulement lorsqu'il tourne.
- **`citeck export ls|get|rm`.** Chaque conteneur dispose désormais d'un répertoire de sortie pour les artefacts — dumps mémoire, dumps de base, rapports — et les fichiers se listent, se téléchargent et se suppriment depuis la CLI.
- **Les applications web Citeck écrivent un dump mémoire en cas d'OutOfMemoryError, sans configuration**, compressé, dans ce répertoire. Seul le dump le plus récent est conservé, pour qu'une boucle de plantages ne remplisse pas le disque.
- **Tous les pools mémoire de la JVM sont maintenant dimensionnés à partir de la limite du conteneur** — tas, mémoire directe, metaspace et cache de code. Auparavant seul le tas était borné : une fuite dans un autre pool épuisait le conteneur, que le noyau tuait sans erreur Java ni dump. Un tas défini à la main est conservé tel quel et les autres pools sont calculés autour ; un conteneur trop petit pour être dimensionné sûrement ne reçoit de plafonds que là où ils ne peuvent pas le casser.
- **Un dump de threads est capturé avant qu'un service ne soit redémarré pour échec de sa sonde de santé**, afin que la cause figure dans le fichier de diagnostic au lieu de disparaître avec le conteneur.

## Corrections
- **Un service dans une longue pause de ramasse-miettes n'est plus redémarré comme mort.** La sonde tolère désormais environ 60 secondes d'échecs continus au lieu d'une vingtaine.
- **Le proxy est recréé en même temps que le gateway.** Après une modification de configuration recréant le gateway, nginx continuait de router vers l'ancienne adresse et les pages derrière le gateway renvoyaient 502 alors que tout semblait sain.
- **`citeck diff` n'annonce plus qu'un rechargement va désactiver HTTPS** sur les namespaces à certificat auto-signé ou Let's Encrypt.
- **Les notes de mise à jour qui ne se chargent pas sont désormais signalées** au lieu d'afficher une fenêtre vide.

## Mise à jour
- Au premier démarrage après la mise à jour, chaque conteneur est recréé une fois : chaque service reçoit le répertoire d'export et les nouveaux réglages JVM. Sur un namespace de 17 services, cela a pris environ 5 minutes.
