## Fehlerbehebungen
- Eine Abhängigkeit, deren Datenvolume gelöscht wurde (zum Beispiel auf der Seite „Volumes“), wird nicht mehr als Upgrade angeboten, dessen Migration dann fehlschlägt: Da keine Daten mehr zu schützen sind, gilt beim nächsten Start die Version aus dem Bundle.
- Wenn eine Migration einen neueren Launcher erfordert, rät `citeck deps` jetzt, `install.sh` erneut auszuführen; `citeck update` aktualisiert nur die Workspace- und Bundle-Definitionen.
- Die Desktop-Warnung vor dem Löschen eines Namespace sagt jetzt, dass auch seine Datenvolumes und sein Ordner samt Snapshots gelöscht werden.
