## Changements
- **Les ports publiés sans adresse n'écoutent désormais que sur 127.0.0.1.** Auparavant, un tel port était ouvert sur toutes les interfaces, et Docker contourne le pare-feu de l'hôte. Seul le proxy est publié sur toutes les interfaces (toujours en mode serveur ; sur le bureau lorsque l'hôte du namespace n'est pas local). Pour ouvrir un port au réseau, indiquez vous-même l'adresse : `'*:15432:5432'` pour toutes les interfaces, ou une adresse précise. Un conteneur en cours d'exécution garde son ancienne liaison jusqu'à sa recréation.
- Une modification de `ports` du proxy enregistrée par une version antérieure ne précise pas d'adresse ; elle signifie donc désormais elle aussi 127.0.0.1 — vérifiez-la avec `citeck edit proxy`.
- Les liens vers RabbitMQ, Mailpit et PgAdmin s'ouvrent sur cette machine (127.0.0.1 / localhost).

## Corrections
- `citeck edit` accepte un port avec adresse (`127.0.0.1:15432:5432`) ; auparavant, un tel port empêchait l'application de démarrer. Un port que le launcher ne sait pas lire est refusé avant tout enregistrement.
- Une modification trop volumineuse, ou une définition sans image, est refusée au lieu d'être enregistrée tronquée.
- Il est de nouveau possible de créer des bases sur les clusters PostgreSQL initialisés par une image plus ancienne (un écart de version de collation après une mise à jour de glibc les bloquait). Le conteneur postgres est recréé une fois.
- Sécurité : le proxy ne laisse plus passer, sous l'utilisateur guest, les requêtes anonymes vers les ressources Alfresco et Share (`/alfresco/…`, `/share/res/…`). Le conteneur du proxy est recréé une fois.
