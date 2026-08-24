## Fehlerbehebungen
- **Windows: Neben dem Launcher öffnet sich kein Konsolenfenster mehr.** Die Anwendung öffnete bei jedem Start ein zweites, schwarzes Fenster; es ist verschwunden, und das eigene Protokoll des Launchers landet nun in `launcher.log` im Protokollordner.
- **Beim Anlegen des Master-Passworts wird jetzt gemeldet, wenn die beiden Eingaben nicht übereinstimmen.** Die Prüfung verhinderte das Fortfahren schon vorher, sagte aber nichts – dadurch wirkte die Schaltfläche „Bestätigen" wirkungslos.

## Verbesserungen
- Die Git-Bibliothek wurde auf 5.19.2 aktualisiert. Damit sind zwei Schwachstellen im Umgang mit Repositories geschlossen: ein Pfadausbruch über manipulierte Referenznamen sowie Arbeitsbaum-Operationen, die symbolischen Verknüpfungen folgten.
