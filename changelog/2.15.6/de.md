## Fehlerbehebungen
- Ein Image aus einem Registry-Pfad, den der Workspace ohne Authentifizierung deklariert (zum Beispiel ein öffentliches Harbor-Projekt), wird jetzt anonym geladen und fragt nicht mehr nach Zugangsdaten, auch wenn dieselbe Registry private Images enthält.
- Die Zugangsdaten einer Registry gehen nicht mehr verloren, wenn der Workspace einen öffentlichen Pfad derselben Registry nach einem privaten aufführt.
