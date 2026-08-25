## Fehlerbehebungen
- **Beim Wechsel von 1.x werden jetzt alle Workspaces und Namespaces übernommen.** Pro Speicherseite wurde nur ein Eintrag gelesen, sodass eine Installation mit 19 Namespaces genau einen migrierte — und die Migration als fehlerfrei meldete. Workspaces, Namespaces, Git-Repositories, der Zustand gestoppter Anwendungen und geänderte Namespace-Dateien werden nun wiederhergestellt. Das behebt die Übernahme selbst; Daten, die bei einem bereits durchgeführten Upgrade verloren gingen, werden nicht automatisch wiederhergestellt — Ihre ursprüngliche 1.x-Datenbank liegt unverändert unter `storage.db.kotlin-bak`, melden Sie sich bei uns, dann kann sie erneut importiert werden.
- **macOS: Das Update blockiert den Launcher nicht mehr.** Nach dem Klick auf „Aktualisieren“ blieb das Fenster bei „Wird aktualisiert…“ stehen, alle Schaltflächen — auch „Abbrechen“ — waren deaktiviert, und der Launcher war bis zum Neustart unbrauchbar. Das Fenster lädt sich jetzt selbst neu, sobald die neue Version läuft, „Abbrechen“ funktioniert immer, und die Schaltflächen verrutschen beim Start des Updates nicht mehr.

## Aktualisierungen
- Mailpit 1.31.0, pgAdmin 9.17 und ZooKeeper 3.9.5.
