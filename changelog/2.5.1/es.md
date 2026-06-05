## Correcciones
- El Inicio rápido «con datos de demostración» ahora carga realmente los datos de demostración; antes el espacio de nombres podía iniciarse con bases de datos vacías.
- El espacio de nombres creado con el Inicio rápido vuelve a llamarse «Citeck Default» en lugar de tomar la etiqueta del botón.
- El panel y la lista de espacios de nombres ahora muestran la versión real del paquete (por ejemplo, 2026.3-RC1) en lugar de «LATEST».
- «Buscar actualizaciones» ya no muestra un error 404 cuando ya tienes la última versión.
- Se corrigieron los errores de autenticación de RabbitMQ tras reiniciar el servidor («no se puede cargar el menú»): el contenedor de RabbitMQ tenía muy poca memoria para terminar de configurar su cuenta de servicio y obtenía una nueva identidad en cada reinicio. Ahora mantiene una identidad estable y dispone de suficiente memoria.
- El archivo de diagnóstico (dump-system-info) ahora oculta contraseñas, tokens y otros secretos de las variables de entorno de los contenedores, los registros y los archivos de configuración.
