## Novedades
- Edita la configuración de una app y sus archivos montados directamente, incluso con el namespace detenido. Tus cambios se guardan como deltas sobre la configuración generada y se vuelven a aplicar en cada regeneración, así las nuevas versiones de imagen y bundle siguen llegando mientras tus cambios se mantienen.
- Una columna de cambios en el editor marca las líneas editadas y añadidas; haz clic en un marcador para revertir esa línea al valor generado.
- Detalles de la imagen: digest (sha256), tamaño y plataforma desde el panel de la app, además de descargar la imagen explícitamente, incluso una etiqueta de versión. Las apps que usan la imagen se actualizan automáticamente tras la descarga.
- Uso de memoria actual y límite de cada app en el panel de detalles.
- Descarga los registros de una app o del daemon directamente a tu carpeta de Descargas, con una notificación y un botón «Abrir carpeta».

## Mejoras
- Protección más fuerte para tu contraseña maestra (derivación de clave Argon2id); los secretos existentes siguen funcionando.
- «Actualizar e iniciar» ahora actualiza los repositorios de bundles antes de iniciar; el selector de bundle muestra todos los repositorios configurados y permite actualizar el seleccionado.
- Diálogos de secretos más claros: sin «Omitir» cuando ya hay una contraseña maestra, el diálogo de migración ofrece un restablecimiento y al crear una contraseña hay un botón «Cancelar».
- Registros más legibles: colores de nivel ajustados para el tema claro y mensajes de error de configuración en monoespaciado.

## Correcciones
- Una app desacoplada ya no se queda atascada en «En cola» tras una actualización forzada.
- Un reinicio manual o aplicar la configuración ya no incrementa el contador de reinicios; el valor del panel coincide con la insignia de la tabla.
- El editor ya no parpadea con un fondo negro o blanco al abrirse.
- Menús contextuales: el primer clic que cierra un menú ya no selecciona una app ni desplaza la fila, y abrir un menú no altera la altura de la fila.
- El botón Detener se muestra mientras la app se actualiza y se desactiva mientras se detiene; el encabezado de la tabla de apps queda fijo al desplazarse.
- En el escritorio, cerrar la aplicación ahora detiene de forma fiable el daemon en segundo plano en todas las plataformas.
