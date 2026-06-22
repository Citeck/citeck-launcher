## Fehlerbehebungen
- Das Stoppen eines Namespace entfernt jetzt alle seine Container sauber — eine langsam stoppende Anwendung (z. B. eapps) bleibt nach dem Herunterfahren nicht mehr im Zustand „Exited", und das Namespace-Netzwerk wird zuverlässig entfernt.
- Das Löschen eines Arbeitsbereichs gibt jetzt alles darin frei — die Docker-Datenvolumes jedes Namespace (PostgreSQL, MongoDB, …) und die Datenbankeinträge, nicht nur die Dateien auf der Festplatte.
- Der integrierte Konfigurationsserver (Desktop) läuft jetzt nur, solange ein Namespace aktiv ist, und gibt seinen Port beim Stoppen frei, sodass ein Neustart der App bei gestopptem Namespace nicht mehr mit „address already in use" fehlschlägt.
