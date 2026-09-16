## Neue Funktionen
- **Qdrant veröffentlicht jetzt seinen gRPC-Port (6334)** — genau den Port, über den `rag` mit ihm spricht; auf 6333 lief immer nur die Health-Prüfung. Ein `rag`, das Sie außerhalb des Launchers (aus der IDE) starten, erreicht den Vektorspeicher dieses Stands nun ganz ohne Konfiguration: seine Standardwerte zeigen bereits auf `localhost:6334`.

## Änderungen
- **Das Stoppen von `rag` löscht Qdrant nicht mehr.** Der Speicher bleibt als gestoppte App mit Start-Schaltfläche im Namespace — genau das braucht man, um `rag` selbst zu betreiben: hier stoppen, dort starten, und die indizierten Daten sind weiterhin erreichbar. Von allein startet er nie: auf jedem Stand, auf dem RAG einfach ausgeschaltet ist, bleibt Qdrant gestoppt und kostet wie bisher keinen Speicher. Starten Sie ihn bewusst (`citeck start qdrant`), läuft er, bis Sie ihn stoppen oder den Launcher neu starten.
- **Dasselbe gilt für `ai` und seinen Speech-to-Text-Sidecar.** Beim Stoppen von `ai` bleibt der Sidecar beschrieben und gestoppt, statt entfernt zu werden — ein aus der IDE gestartetes `ai` findet die Spracherkennung weiterhin auf localhost.
- **Das RAG-Flag des Assistenten richtet sich jetzt danach, OB der Namespace `rag` hat**, nicht danach, ob `rag` gerade läuft. Das Umschalten von `rag` schreibt und erstellt den `ai`-Container nicht mehr neu.
