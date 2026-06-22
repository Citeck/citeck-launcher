## Correcciones
- Al detener un namespace ahora se eliminan limpiamente todos sus contenedores: una aplicación de cierre lento (por ejemplo, eapps) ya no queda en estado «Exited» tras el apagado, y la red del namespace se elimina de forma fiable.
- Al eliminar un espacio de trabajo ahora se recupera todo lo que contiene: los volúmenes de datos de Docker de cada namespace (PostgreSQL, MongoDB, …) y los registros de la base de datos, no solo los archivos en disco.
- El servidor de configuración integrado (escritorio) ahora se ejecuta solo mientras un namespace está activo y libera su puerto al detenerse, de modo que reiniciar la aplicación con un namespace detenido ya no falla con «address already in use».
