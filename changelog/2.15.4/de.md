## Fehlerbehebungen
- Stoppt der Container einer App nicht innerhalb ihres Stopp-Timeouts (zum Beispiel ein Java-Dienst, der das Stoppsignal ignoriert), wird er jetzt zwangsweise entfernt und die App erreicht „Gestoppt“, statt mit nicht abrufbaren Logs in „Stoppen fehlgeschlagen“ hängen zu bleiben.
- Eine App, die Sie selbst gestoppt haben und die in „Stoppen fehlgeschlagen“ hängt, wiederholt das Stoppen jetzt selbstständig und erreicht „Gestoppt“; sie wird nicht erneut gestartet.
