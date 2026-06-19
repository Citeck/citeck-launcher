## Neue Funktionen
- Bearbeiten Sie die Arbeitsbereich-Konfiguration (workspace-v1.yml) direkt vom Willkommensbildschirm aus — ein Zahnrad neben der Arbeitsbereich-Auswahl öffnet einen YAML-Editor mit Änderungsmarkierungen. Ihre Änderungen werden als Delta über der Git-Referenz gespeichert und bei jeder Synchronisierung erneut angewendet.
- Wählen Sie beim Erstellen eines Snapshots genau aus, welche Volumes einbezogen werden.

## Fehlerbehebungen
- Status in der Desktop-App werden jetzt auf allen Plattformen in Echtzeit aktualisiert; unter Windows wirkten sie während eines langen Starts nicht mehr eingefroren (z. B. alle auf „Wird geladen“), bis „Stoppen“ gedrückt wurde.
- Unter Windows wird das Fenster nach dem Bestätigen des Master-Passworts oder dem Schließen eines verschachtelten Dialogs nicht mehr unklickbar.
- Fehlgeschlagene Image-Pulls protokollieren jetzt die Ursache (z. B. eine abgelehnte Registry-Verbindung), sodass ersichtlich ist, warum ein Pull nicht durchging.
- Die Namespace-Liste zeigt lokalisierte Status, und gelöschte Namespaces verschwinden aus der Schnellstart-Liste.
- Der Willkommensbildschirm ist im hellen Design lesbar, und native Kontrollkästchen werden in beiden Designs korrekt und ausgerichtet dargestellt.
- Die Namespace-Kopfzeile wird nach einer Umbenennung zuverlässig aktualisiert, und die Auswahl eines Werts in einem Dropdown verschiebt das Layout nicht mehr.
- Der Installer startet die Anwendung nach einer Windows-Installation oder einem Upgrade jetzt automatisch.
