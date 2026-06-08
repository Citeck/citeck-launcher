## Neue Funktionen
- Der Launcher warnt, wenn der Docker-Datenordner wenig Speicherplatz hat, bevor Container ausfallen.

## Fehlerbehebungen
- „Aktualisierung erzwingen und starten“ reagiert jetzt sofort und erzwingt über Git eine Prüfung auf neue Bundle-Versionen, während bereits heruntergeladene Release-Images wiederverwendet statt erneut geladen werden.
- Anwendungen, die beim Stoppen hängen blieben, erholen sich jetzt von selbst und blockieren den Namespace nicht mehr.
- Das Neuladen eines Namespace bleibt nicht mehr unbegrenzt hängen, wenn eine Git-Operation stockt.
- Automatische Container-Neustarts verwenden das lokale Image wieder, sodass sie nie unbemerkt die Version wechseln oder bei einem Registry-Ausfall fehlschlagen.
- Übrig gebliebene Docker-Container, -Volumes und -Netzwerke gelöschter Namespaces werden jetzt beim Start automatisch bereinigt.
- Der aktive Namespace zeigt immer seine eigenen Container, ohne Namespace-Verwechslung.
- Zusätzliche Fenster (Protokolle, Editor) schließen sich jetzt, wenn Sie die App in die Taskleiste minimieren oder zum Willkommensbildschirm zurückkehren.

## Änderungen
- Namespaces werden über das Formular bearbeitet; der Roh-YAML-Editor und das Kontextmenü des Zahnrads wurden entfernt.
