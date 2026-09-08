## Neue Funktionen
- **Die Versionen der Infrastruktur sind jetzt an die Daten gebunden.** Ein Bundle-Update kann PostgreSQL, RabbitMQ, ZooKeeper, Keycloak oder MongoDB nicht mehr auf eine Version heben, die die vorhandenen Daten eines Namespace unbrauchbar machen würde. Der Launcher bleibt bei der Version, mit der die Daten angelegt wurden, und meldet die neue stattdessen — als Banner und im neuen Dialog **Abhängigkeiten** der Desktop-App, auf dem Server über `citeck deps`.
- **Migration von PostgreSQL 17 auf 18** — aus dem Dialog „Abhängigkeiten“ oder mit `citeck deps upgrade postgres`. Der Namespace wird gestoppt, die Daten werden exportiert, in einem **neuen** Volume entsteht ein neuer Cluster, dorthin wird wiederhergestellt und geprüft, danach schaltet der Namespace um — mit Fortschritt für jeden Schritt. Jeder Fehler wird automatisch auf die alten Daten zurückgerollt.
- Das bisherige Daten-Volume **bleibt** nach einer erfolgreichen Migration erhalten (es steht auf der Seite „Volumes“); löschen Sie es selbst, sobald Sie mit dem Ergebnis zufrieden sind.
- Liegt noch ein Ziel-Volume aus einem früheren Versuch vor, zeigt der Launcher dessen Größe und Version an und löscht es erst nach Ihrer Bestätigung.

## Änderungen
- Neue Namespaces werden mit PostgreSQL 18 angelegt; bestehende bleiben bis zur Migration auf PostgreSQL 17.
- Aktualisierungen von RabbitMQ, ZooKeeper, Keycloak und MongoDB werden gemeldet, aber noch nicht ausgeführt — sie warten auf eine Launcher-Version, die sie migrieren kann.
- Das Image einer Abhängigkeit auf eine für ihre Daten inkompatible Version zu ändern, wird abgelehnt — im Konfigurationseditor wie in `citeck edit`.
- Starten, Neuladen und Löschen eines Namespace werden abgelehnt, während ein Snapshot oder eine Abhängigkeits-Migration läuft; die Meldung nennt den Grund.
