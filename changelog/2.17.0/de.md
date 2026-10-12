## Neue Funktionen
- Beim Erstellen eines Namensraums kann DB oder S3 als Inhaltsspeicher gewählt werden; die S3-Verfügbarkeit hängt vom Bundle und der Edition ab.
- Agent-API und Erkennungsbefehle ohne Daemon-Start beschreiben unterstützte Launcher-Operationen.
- Bei Git-Aktualisierungsfehlern stehen Cache, Wiederholung und eine Token-Auswahl pro Repository zur Verfügung; die letzte erfolgreiche Synchronisierung wird angezeigt.
- Updates und Snapshot-Downloads zeigen empfangene Bytes und setzen unterbrochene Übertragungen automatisch fort.

## Fehlerbehebungen
- Aktive Downloads laufen weiter, solange Bytes eintreffen, ohne Begrenzung der Gesamtdauer.
- Die normale S3-Initialisierung zeigt kein Banner mehr; Konfigurations- und Verbindungsfehler bleiben sichtbar.
- Mailpit nutzt die Host-Ports 17011 für SMTP und 17012 für die Weboberfläche.
- Git-Diagnosen verbergen Zugangsdaten in Repository-URLs.
