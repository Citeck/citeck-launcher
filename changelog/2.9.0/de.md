## Neu
- **HTTPS für einen Namespace (selbstsigniert).** Eine neue Option „HTTPS aktivieren (selbstsigniert)" im Namespace-Dialog stellt den Proxy über HTTPS mit einem automatisch erzeugten Zertifikat bereit. Der Port folgt automatisch dem Schema — 443 für HTTPS, 80 für HTTP — sodass das Ein- und Ausschalten von HTTPS einfach funktioniert. Der Browser zeigt beim ersten Mal eine Warnung zum selbstsignierten Zertifikat.

## Änderungen
- **Konfigurationsänderungen sind gezielt und sofortig.** Das Bearbeiten der Einstellungen eines Namespace oder einer App erstellt jetzt nur noch die Container neu, deren Konfiguration sich tatsächlich geändert hat, und zeigt die Änderung sofort an — Images unbeteiligter Dienste werden nicht mehr neu geladen. Verwenden Sie „Aktualisieren und starten", um die neuesten Images zu holen.
- **Der Start wartet auf das Master-Passwort.** Wenn ein Namespace aus einer privaten Registry lädt und Ihre Secrets gesperrt sind, wartet er nun auf die Eingabe des Master-Passworts, bevor er startet — sodass der Anmeldedialog den Entsperr-Bildschirm nicht mehr verdeckt.
