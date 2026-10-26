## Correcciones
- La importación y exportación de instantáneas muestran el progreso y recuperan el resultado tras una interrupción de conexión, sin dejar bloqueada la pantalla de carga.
- La migración de PostgreSQL tras restaurar una instantánea reconoce las ubicaciones de datos antiguas y descarga automáticamente la imagen de origen que falte.
- Desbloquear los secretos actualiza los namespaces detenidos para que S3 y las aplicaciones que usan secretos estén disponibles sin otro reinicio.
- Los avisos de inicio permanecen ocultos hasta completar la contraseña maestra y la actualización posterior.
- Las migraciones y reversiones de dependencias completadas ya no dejan girando el último paso ni bloquean los controles del namespace.
- Los eventos de escritorio conservan su orden; la sincronización normal ya no genera avisos repetidos de «Conexión restablecida».
- Las herramientas de administración de Alfresco Share vuelven a funcionar en entornos con Alfresco.
- La verificación HTTPS rechaza certificados intermedios comprometidos conocidos.
