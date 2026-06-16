## Neue Funktionen
- Zoomen Sie die Oberfläche mit den nativen WebView-Zoomsteuerungen.
- Einzelne App über ein Rechtsklick-Menü neu starten – in jedem App-Zustand verfügbar.

## Fehlerbehebungen
- Die Server-Installation mit Demodaten importiert jetzt tatsächlich den Demo-Snapshot – zuvor wurde der Namespace zwar angelegt, startete aber mit leeren Daten.
- Die Server-Installation speichert nun den bei der Einrichtung eingegebenen Login für die private Registry, statt ihn stillschweigend zu verwerfen.
- Mehr Übereinstimmung mit dem 1.x-Launcher für migrierte oder von Hand bearbeitete Konfigurationen: Standard-Namespace-Benutzer (admin + fet), Bundle-/Workspace-Einstellungen sowie Probe-/Log-Startup-Standardwerte.
- Vor dem Hinzufügen des ersten Geheimnisses werden Sie nun zur Festlegung eines Master-Passworts aufgefordert, statt eine Fehlermeldung zu erhalten.
- Quick Start bindet die neueste Version an eine konkrete Release, aktualisiert die verfügbaren Versionen nach dem Entsperren der Geheimnisse und öffnet das Namespace-Panel direkt beim Start.
- Eigene Workspace-Repositorys erholen sich automatisch, nachdem Sie ihr Zugriffstoken hinzugefügt oder korrigiert haben – kein Neustart nötig.
- Gespeicherte Registry-Anmeldedaten werden sofort an ihren Registry-Host gebunden.
- Geheimniswerte werden jetzt klar als Token oder Password gekennzeichnet.
- Snapshot-Dialog: eine gemeinsame Kopfzeile und eine automatische Aktualisierung, sobald ein Export oder Import abgeschlossen ist.
- Kontrastreicheres App-Detailpanel; der Konfigurationseditor folgt jetzt dem App-Theme.
- Besser lesbarer Log-Text und ein Hinweis, warum „Alle löschen" deaktiviert ist.
- Beim Löschen eines Workspace werden nun auch dessen Registry-Anmeldedaten-Bindungen entfernt.
