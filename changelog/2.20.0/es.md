## Nuevas funciones

- Configuración de servicios complementarios para aplicaciones web (`webapps[].companions`): los workspaces pueden asignar variables de entorno y dependencias de arranque a las aplicaciones web solo mientras el servicio complementario (como una pasarela LLM opcional) esté presente en el entorno, manteniendo la prioridad de los ajustes del namespace.
- Bases de datos en el PostgreSQL principal del entorno para aplicaciones adicionales (`additionalApps[].postgresDatabases`): los servicios personalizados pueden solicitar bases de datos en el clúster principal de PostgreSQL para que participen automáticamente en migraciones, snapshots y pgAdmin sin recrear PostgreSQL al activar o desactivar funciones.
- Descripciones emergentes localizadas para enlaces personalizados (`links[].descriptions`): los enlaces del workspace ahora admiten descripciones por idioma con compatibilidad hacia atrás, y la interfaz los actualiza al instante al cambiar de idioma.
- Se añadió el icono de LiteLLM para la barra lateral y se actualizó el icono de Mailpit.
