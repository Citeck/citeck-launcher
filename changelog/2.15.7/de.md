## Fehlerbehebungen
- Das Upgrade von einem frühen 1.x-Launcher verhindert den Start des Launchers nicht mehr: Ein Namespace, der seine Benutzer als eine Zeile „Name:Passwort“ gespeichert hat, wird mit diesen Benutzernamen migriert.
- Ein in einem solchen Namespace gesetztes Proxy-Image wird übernommen, statt verloren zu gehen.
- Ein Namespace aus einem Launcher älter als 1.3.6 behält sein zuletzt aufgelöstes Bundle und startet daher auch dann, wenn dieses Bundle im Bundle-Repository inzwischen verschoben wurde.
