## Fehlerbehebungen
- Behoben, dass auf macOS der Bildschirm „Docker nicht verfügbar“ erschien, obwohl Docker Desktop lief. Der Launcher erkennt Docker jetzt genauso wie die `docker`-CLI – über den aktiven Docker-Kontext (Docker Desktop, colima, Rancher Desktop) – und greift bei Bedarf auf weitere gängige Socket-Pfade zurück, statt nur den Standardpfad zu prüfen.
