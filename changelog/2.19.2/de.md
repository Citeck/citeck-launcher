## Verbesserungen

- MCP-Agenten mit der Entwicklerrolle können jetzt die vollständige Container-Konfiguration einer Anwendung bearbeiten, einschließlich Images, Befehlen, Mounts, Zustandsprüfungen, Abhängigkeiten und öffentlichen Ports. Dabei gelten dieselben Validierungen wie in der Benutzeroberfläche.
- Die Dateibearbeitung über MCP unterstützt jetzt eingebundene Textdateien der Anwendung, einschließlich Skripten, Proxy-Konfiguration und verschachtelten Dateien, statt nur Spring-YAML.

## Fehlerbehebungen

- Ein durch eine Race Condition bei der Verarbeitung übergroßer Anfragen verursachter Absturz des HTTP/1-Servers wurde behoben (Go 1.27.4, CVE-2026-97054).
