## Neue Funktionen
- **Automatische Updates funktionieren jetzt auch unter macOS und Windows**, nicht mehr nur unter Linux. Der Launcher lädt die neue Version selbst herunter, prüft ihre Signatur und installiert sie.

## Fehlerbehebungen
- **Ein abgebrochenes Update macht den Launcher nicht mehr startunfähig.** Ein Update, das heruntergeladen, aber nie geprüft wurde — weil die App zwischendurch geschlossen wurde oder abgestürzt ist —, wird beim nächsten Start geprüft und automatisch zurückgenommen, wenn es nicht hochkommt.
- **Alfresco und Solr erhalten keine JVM-Optionen mehr, die ihr Java 8 nicht kennt.** Eine unbekannte Option wird nicht ignoriert: Die JVM startet dann überhaupt nicht.
- **Alfresco und Solr bemessen ihren Speicher am Container-Limit.** Der Heap von Solr war exakt so groß wie sein eigener Container, was damit endet, dass der Kernel den Prozess beendet; Alfresco hatte gar kein Limit. Solr hat jetzt 2560 MB, Alfresco 8 GB.
- **Dienste belegen beim Start nicht mehr ihren gesamten Heap.** `heapSize` legt nur noch das Maximum fest, sodass ein großer Namespace den Speicher erst bei Bedarf nimmt.

## Verbesserungen
- Das Daemon-Log wiederholt die JVM-Speicherberechnung nicht mehr für jeden Dienst bei jedem Reload.
- Gebaut mit Go 1.26.6, das Sicherheitskorrekturen für die Standardbibliothek enthält.

## Beim Update
- Beim ersten Start nach dem Update werden die Java-Container einmalig neu erstellt.
