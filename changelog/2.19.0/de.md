## Neue Funktionen

- Lokale KI-Agenten können sich über MCP verbinden, um Status und Protokolle einzusehen. Die Rolle Developer erlaubt außerdem das Verwalten von Anwendungen, das Bearbeiten von Laufzeiteinstellungen und Spring-Konfiguration sowie das Erfassen von JVM-Diagnosen.
- Agentenzugriff in den Einstellungen der Desktop-Anwendung verwalten: OAuth-Anmeldungen genehmigen oder manuelle Tokens erstellen, Observer oder Developer wählen, die Gültigkeit festlegen und Zugriffe widerrufen. MCP ist standardmäßig deaktiviert und lauscht nach dem Aktivieren nur auf der Loopback-Schnittstelle.
- Im Konfigurationseditor lassen sich CPU-Limits je Anwendung in Kernen festlegen, auch mit Bruchteilen.

## Fehlerbehebungen

- Beim Neuladen werden Schreibfehler bei generierten Konfigurationsdateien jetzt gemeldet, statt mit nicht gespeicherten Dateien fortzufahren.
- Bearbeitete Konfigurationsdateien werden nicht mehr stillschweigend durch leeren Inhalt ersetzt, wenn die aktuelle Datei nicht verfügbar ist und gespeicherte Änderungen nicht erneut angewendet werden können.
- Änderungen an YAML-Konfigurationen erhalten alle Dokumente in Dateien mit mehreren YAML-Dokumenten.
- JVM-Diagnosebefehle werden nicht mehr automatisch wiederholt, wenn sie die JVM möglicherweise bereits erreicht haben.
