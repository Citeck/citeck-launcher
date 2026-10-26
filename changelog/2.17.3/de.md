## Fehlerbehebungen
- Snapshot-Import und -Export zeigen den aktuellen Fortschritt und erkennen den Abschluss nach einer Verbindungsunterbrechung, ohne im Ladebildschirm hängen zu bleiben.
- Die PostgreSQL-Migration nach einer Snapshot-Wiederherstellung erkennt ältere Datenverzeichnisse und lädt ein fehlendes Ausgangsimage automatisch herunter.
- Das Entsperren der Geheimnisse aktualisiert gestoppte Namespaces, sodass S3 und andere Anwendungen mit Geheimnissen ohne erneuten Neustart verfügbar werden.
- Starthinweise bleiben bis zum Abschluss der Master-Passworteingabe und der anschließenden Aktualisierung ausgeblendet.
- Abgeschlossene Migrationen und Rollbacks von Abhängigkeiten lassen weder den letzten Schritt weiterlaufen noch die Namespace-Steuerung gesperrt.
- Desktop-Ereignisse behalten ihre Reihenfolge; die normale Statussynchronisierung erzeugt keine wiederholten Meldungen „Verbindung wiederhergestellt“ mehr.
- Die Administrationswerkzeuge von Alfresco Share funktionieren wieder auf Umgebungen mit Alfresco.
- Die HTTPS-Prüfung weist bekannte kompromittierte Zwischenzertifikate zurück.
