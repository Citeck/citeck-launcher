## Novedades
- El lanzador avisa cuando el directorio de datos de Docker se queda sin espacio en disco, antes de que los contenedores empiecen a fallar.

## Correcciones
- «Forzar actualización e iniciar» ahora responde al instante y fuerza una comprobación de nuevas versiones de paquetes mediante Git, reutilizando las imágenes de versión ya descargadas en lugar de volver a descargarlas.
- Las aplicaciones que se quedaban bloqueadas al detenerse ahora se recuperan solas y dejan de congelar el espacio de nombres.
- La recarga de un espacio de nombres ya no se queda colgada indefinidamente si una operación de Git se estanca.
- Los reinicios automáticos de contenedores reutilizan la imagen local, por lo que nunca cambian de versión de forma silenciosa ni fallan cuando el registro no está disponible.
- Los contenedores, volúmenes y redes de Docker que quedaban de espacios de nombres eliminados ahora se limpian automáticamente al iniciar.
- El espacio de nombres activo siempre muestra sus propios contenedores, sin discrepancias entre espacios de nombres.
- Las ventanas adicionales (registros, editor) ahora se cierran al minimizar a la bandeja o volver a la pantalla de bienvenida.

## Cambios
- Los espacios de nombres se editan mediante el formulario; se han eliminado el editor de YAML sin formato y el menú contextual del engranaje.
