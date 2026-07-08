## Fehlerbehebungen
- Log-Viewer: Das Ende des Logs ändert sich nicht mehr je nach gewählter Zeilenanzahl (200 / 500 / 1000 / 5000). Das Container-Log wird jetzt in echter chronologischer Reihenfolge angezeigt — die stderr-Zeilen eines Containers (etwa die Startwarnungen einer Java-Anwendung) werden nicht mehr ans Ende des Logs verschoben, unabhängig davon, wann sie geschrieben wurden.
