## Correcciones
- El servicio de IA (grabación de llamadas / reconocimiento de voz) ahora es accesible a través del proxy, y la vinculación con el sidecar STT se aplica correctamente.
- Adjuntar o desconectar la IA, el sidecar STT u OnlyOffice en tiempo de ejecución ahora surte efecto de inmediato: el proxy y los enlaces de IA se actualizan sin volver a crear el namespace.
- Los microservicios externos que se ejecutan fuera de Docker pueden volver a acceder al servidor de configuración en la nube del escritorio (para obtener las direcciones de RabbitMQ, ZooKeeper y base de datos); antes escuchaba por error solo en loopback.
- El aviso para «introducir las credenciales del registro» ahora reaparece de forma fiable cuando una descarga de imagen falla con un error de autenticación, incluso si se perdió la notificación única.
- Eliminar una credencial de registro ya no deja un vínculo huérfano «(no encontrado)» para su host.
- La ventana del editor de configuración del espacio de trabajo es más grande y legible, y «Restablecer a git» ahora se aplica con un solo clic.
- Las celdas de CPU y memoria ya no mantienen un resaltado atascado tras una selección accidental.
- El instalador de Windows ya no se queda colgado en «Cancelando…» tras una instalación o actualización, lo que podía bloquear otros instaladores.
