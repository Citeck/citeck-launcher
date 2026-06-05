## Fehlerbehebungen
- Aus Version 1.x migrierte Namespaces werden beim ersten Start nicht mehr beschädigt: Ein unerwünschter automatischer erneuter Snapshot-Import, der die Datenbank beschädigen konnte, wurde entfernt. Das Importieren eines Snapshots ist jetzt immer eine manuelle Aktion.
- Nach dem Wechsel des aktiven Namespace zeigt die App nun die Container dieses Namespace — Kopfzeile und Anwendungsdetails widersprechen sich nicht mehr.
- Beim Löschen eines Namespace werden jetzt auch dessen Docker-Volumes, -Netzwerk und -Container entfernt, sodass keine Restdaten mehr zurückbleiben.
- Die CPU-Auslastung der Container wird wieder korrekt angezeigt (sie blieb zuvor bei 0 %).
- Die Protokollanzeige friert nach einem Container-Neustart nicht mehr ein — sie verbindet sich automatisch neu.
- Das Formular zum Erstellen/Bearbeiten von Namespaces ist aufgeräumter: Bundle-Repositories ohne Releases werden ausgeblendet, und beim Wechsel des Repositories wird automatisch die neueste Version ausgewählt.
- Der Titel des Desktop-Fensters spiegelt nun die laufende Version wider.
- Daemon-Protokolle werden in einem einzigen Ordner „logs“ gespeichert (wie in Version 1.x), statt nach einem Update auf zwei Ordner verteilt zu werden.
