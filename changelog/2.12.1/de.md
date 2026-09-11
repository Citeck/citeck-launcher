## Neue Funktionen
- **Ein Bundle oder eine Workspace-Konfiguration kann die Images von Drittanbieter-Komponenten in einem neuen Abschnitt `dependencies:` benennen.** Launcher älter als 2.12 lesen diesen Abschnitt nicht, deshalb erreicht eine dort angehobene Version von PostgreSQL, RabbitMQ oder einer anderen Infrastruktur nur jene Launcher, die die vorhandenen Daten schützen können — alle anderen laufen unverändert weiter. In der Workspace-Konfiguration hebt der Abschnitt die Version zugleich für alle Namespaces dieses Workspace an.

## Fehlerbehebungen
- **Ein Desktop-Update wird nicht mehr zurückgerollt, nur weil Docker langsam war.** Der Daemon antwortet jetzt, sobald sein Prozess lebt, und nicht erst am Ende des Starts — so beurteilt die Update-Prüfung die neue Version und nicht die Startdauer des Rechners. Unter Windows gemeldet: ein einwandfreies Release wurde installiert, nach 60 Sekunden Warten auf ein nicht reagierendes Docker Desktop für „startet nicht“ erklärt und zurückgerollt.
- **Ein fehlgeschlagenes Update lässt sich erneut installieren.** Eine einmal gescheiterte Version wurde bisher für immer abgelehnt; das Update-Fenster bietet nun **Erneut versuchen** an, während der Launcher sie von sich aus weiterhin nie wiederholt.
- Das Update-Fenster behauptet nicht mehr, Sie seien auf der neuesten Version, während es ein fehlgeschlagenes Update meldet, und bietet nicht mehr an, ein zurückgerolltes Update durch einen Neustart „fertig zu installieren“.
- `citeck reload` auf einem gestoppten Namespace wartet nicht mehr endlos: die Konfiguration wird neu geladen, und der Befehl sagt, dass sie beim nächsten Start greift. Kein Warten auf einen Namespace oder den Daemon kann mehr unbegrenzt hängen.

## Änderungen
- pgAdmin wählt sein Image jetzt genauso wie jede andere Drittanbieter-App. Auf einem Desktop-Stand, in dem sowohl die Workspace-Konfiguration als auch das Bundle ein pgAdmin-Image benennen, gewinnt das Bundle — der Container wird einmalig neu erstellt.
- Ein fehlgeschlagenes Update nennt seinen Grund jetzt im Log, der Daemon vermerkt beim Start die laufende Version, und der System-Dump enthält das Log des Launchers selbst sowie das Update-Manifest.
