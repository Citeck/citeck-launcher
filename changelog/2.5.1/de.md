## Fehlerbehebungen
- Der Schnellstart „mit Demodaten“ lädt jetzt tatsächlich die Demodaten – zuvor konnte der Namespace mit leeren Datenbanken starten.
- Der per Schnellstart erstellte Namespace heißt wieder „Citeck Default“, statt die Beschriftung der Schaltfläche zu übernehmen.
- Das Dashboard und die Namespace-Liste zeigen nun die echte Bundle-Version (zum Beispiel 2026.3-RC1) anstelle von „LATEST“ an.
- „Nach Updates suchen“ zeigt keinen 404-Fehler mehr an, wenn Sie bereits die neueste Version verwenden.
- RabbitMQ-Authentifizierungsfehler nach einem Server-Neustart („Menü kann nicht geladen werden“) behoben: Der RabbitMQ-Container hatte zu wenig Arbeitsspeicher, um die Einrichtung seines Dienstkontos abzuschließen, und erhielt bei jedem Neustart eine neue Identität. Er behält nun eine stabile Identität und verfügt über genügend Arbeitsspeicher.
- Das Diagnosearchiv (dump-system-info) maskiert jetzt Passwörter, Tokens und andere Geheimnisse in Container-Umgebungsvariablen, Protokollen und Konfigurationsdateien.
- Die Weboberfläche ist jetzt vollständig ins Chinesische, Spanische, Deutsche, Französische, Portugiesische und Japanische übersetzt – viele zuvor englische Texte sind nun lokalisiert.
- Das Wiederherstellen eines Namespace-Snapshots beschädigt die Datenbank nicht mehr — die Wiederherstellung beginnt nun mit einem leeren Volume, statt sich über die vorhandenen Daten zu legen.
