## Neue Funktionen

- Begleitdienst-Einstellungen für Webapps (`webapps[].companions`): Workspaces können Umgebungsvariablen und Startabhängigkeiten nur solange an Webapps übergeben, wie der zugehörige Begleitdienst (z. B. ein optionales LLM-Gateway) auf der Umgebung vorhanden ist, wobei Namespace-Überschreibungen Vorrang behalten.
- Datenbanken im Haupt-PostgreSQL der Umgebung für zusätzliche Dienste (`additionalApps[].postgresDatabases`): Benutzerdefinierte Dienste können Datenbanken im Haupt-PostgreSQL-Cluster anfordern, sodass sie automatisch an Migrationen, Snapshots und pgAdmin teilnehmen, ohne PostgreSQL beim Umschalten von Funktionen neu zu erstellen.
- Lokalisierte Kurzinfos für benutzerdefinierte Seitenleisten-Links (`links[].descriptions`): Workspace-Links unterstützen jetzt sprachabhängige Tooltips mit abwärtskompatiblem Fallback, und die Oberfläche aktualisiert sie beim Sprachwechsel sofort.
- LiteLLM-Symbol für die Seitenleiste hinzugefügt und das Mailpit-Symbol aktualisiert.
