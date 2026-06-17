## Neue Funktionen
- Bearbeiten Sie die Konfiguration einer App und ihre eingebundenen Dateien direkt — auch wenn der Namespace gestoppt ist. Ihre Änderungen werden als Deltas über der generierten Konfiguration gespeichert und bei jeder Regenerierung erneut angewendet, sodass neue Image- und Bundle-Versionen weiterhin durchkommen, Ihre Änderungen aber erhalten bleiben.
- Eine Änderungsspalte im Editor markiert geänderte und hinzugefügte Zeilen; ein Klick auf einen Marker setzt die Zeile auf den generierten Standard zurück.
- Image-Details: Digest (sha256), Größe und Plattform direkt im App-Panel, plus explizites Laden eines Images — auch eines Release-Tags. Apps, die das Image nutzen, werden nach dem Laden automatisch aktualisiert.
- Aktuelle Speichernutzung und das Limit jeder App im Detailpanel.
- Laden Sie die Logs einer App oder des Daemons direkt in Ihren Downloads-Ordner — mit Benachrichtigung und „Ordner öffnen“-Schaltfläche.

## Aktualisierungen
- Stärkerer Schutz für Ihr Master-Passwort (Argon2id-Schlüsselableitung); vorhandene Secrets funktionieren weiter.
- „Aktualisieren und Starten“ aktualisiert nun die Bundle-Repositorys vor dem Start; die Bundle-Auswahl listet alle konfigurierten Repositorys und lässt das ausgewählte aktualisieren.
- Klarere Secret-Dialoge: kein „Überspringen“, wenn ein Master-Passwort gesetzt ist; der Migrationsdialog bietet ein Zurücksetzen; beim Erstellen eines Passworts gibt es „Abbrechen“.
- Besser lesbare Logs: Stufenfarben für das helle Theme abgestimmt und Konfigurationsfehler in Monospace.

## Fehlerbehebungen
- Eine abgetrennte App bleibt nach einem erzwungenen Update nicht mehr in „In der Warteschlange“ hängen.
- Ein manueller Neustart oder ein Anwenden der Konfiguration erhöht den Neustart-Zähler nicht mehr; der Wert im Panel stimmt mit dem Badge in der Tabelle überein.
- Der Editor blendet beim Öffnen keinen schwarzen oder weißen Hintergrund mehr auf.
- Kontextmenüs: Der erste Klick, der ein Menü schließt, wählt keine App mehr aus und verschiebt die Zeile nicht, und das Öffnen eines Menüs ändert die Zeilenhöhe nicht.
- Die Stopp-Schaltfläche wird beim Aktualisieren angezeigt und beim Stoppen deaktiviert; die Kopfzeile der App-Tabelle bleibt beim Scrollen fixiert.
- Auf dem Desktop stoppt das Schließen der App den Hintergrund-Daemon nun zuverlässig auf allen Plattformen.
