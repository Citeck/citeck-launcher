## Nuevas funciones
- Al crear un espacio de nombres se puede elegir DB o S3; la disponibilidad de S3 depende del paquete y la edición seleccionados.
- La API para agentes y los comandos de descubrimiento sin iniciar el daemon describen las operaciones de Launcher compatibles.
- La recuperación de actualizaciones Git permite usar la caché, reintentar y elegir un token por repositorio; muestra la última sincronización correcta.
- Las descargas de actualizaciones e instantáneas muestran los bytes recibidos y reanudan automáticamente las transferencias interrumpidas.

## Correcciones
- Las descargas activas continúan mientras llegan bytes, sin límite de duración total.
- La inicialización normal de S3 ya no muestra un aviso; los errores de configuración y conexión siguen visibles.
- Mailpit utiliza los puertos del host 17011 para SMTP y 17012 para su interfaz web.
- Los diagnósticos de Git ocultan las credenciales incluidas en las URL de los repositorios.
