## Neue Funktionen
- **JVM-Diagnose ohne JDK im Image.** `citeck jstack <app>` für einen Thread-Dump, `citeck jmap <app>` für einen Heap-Dump, der auf Ihren Rechner heruntergeladen wird, `citeck jcmd <app> <Befehl>` für alles Übrige, was die JVM selbst meldet. Das funktioniert auch, während ein Dienst startet oder ausfällt, nicht nur im laufenden Betrieb.
- **`citeck export ls|get|rm`.** Jeder Container hat jetzt ein Ausgabeverzeichnis für Artefakte – Heap-Dumps, Datenbank-Dumps, Berichte –, und die Dateien werden über die CLI aufgelistet, heruntergeladen und gelöscht.
- **Citeck-Webanwendungen schreiben bei OutOfMemoryError von Haus aus einen Heap-Dump**, komprimiert, in dieses Verzeichnis. Es wird nur der neueste Dump aufbewahrt, damit eine Absturzschleife die Festplatte nicht füllt.
- **Alle JVM-Speicherbereiche werden jetzt aus dem Container-Limit berechnet** – Heap, Direct Memory, Metaspace und Code Cache. Bisher war nur der Heap begrenzt; ein Leck in einem anderen Bereich erschöpfte deshalb den Container, und der Kernel beendete ihn ohne Java-Fehler und ohne Dump. Ein von Hand gesetzter Heap bleibt unverändert, die übrigen Bereiche werden darum herum bemessen; ein Container, der sich nicht sicher bemessen lässt, erhält Obergrenzen nur dort, wo sie ihm nicht schaden können.
- **Vor dem Neustart eines Dienstes, der seine Health-Prüfung nicht besteht, wird ein Thread-Dump erstellt**, sodass die Ursache in der Diagnosedatei landet, statt mit dem Container zu verschwinden.

## Fehlerbehebungen
- **Ein Dienst in einer langen Garbage-Collection-Pause wird nicht mehr als tot neu gestartet.** Die Health-Prüfung toleriert jetzt rund 60 Sekunden ununterbrochener Fehlschläge statt etwa 20.
- **Der Proxy wird zusammen mit dem Gateway neu erstellt.** Nach einer Konfigurationsänderung, die das Gateway neu erstellte, leitete nginx weiter an die alte Adresse, und Seiten hinter dem Gateway lieferten 502, obwohl alles gesund aussah.
- **`citeck diff` behauptet nicht mehr, ein Reload würde HTTPS abschalten** – bei Namespaces mit selbstsigniertem oder Let's-Encrypt-Zertifikat.
- **Update-Hinweise, die sich nicht laden lassen, werden jetzt gemeldet**, statt einen leeren Dialog zu zeigen.

## Beim Update
- Beim ersten Start nach dem Update wird jeder Container einmal neu erstellt: Jeder Dienst erhält das Export-Verzeichnis und die neuen JVM-Einstellungen. Bei einem Namespace mit 17 Diensten dauerte das etwa 5 Minuten.
