## Cambios
- **Los puertos publicados sin dirección ahora escuchan solo en 127.0.0.1.** Antes, un puerto así quedaba abierto en todas las interfaces, y Docker se salta el cortafuegos del host. Solo el proxy se publica en todas las interfaces (siempre en modo servidor; en el escritorio cuando el host del namespace no es local). Para abrir un puerto a la red, indique usted la dirección: `'*:15432:5432'` para todas las interfaces, o una concreta. Un contenedor en ejecución conserva su enlace anterior hasta que se recrea.
- Una edición de `ports` del proxy guardada por una versión anterior no indica dirección, así que ahora también significa 127.0.0.1: revísela con `citeck edit proxy`.
- Los enlaces a RabbitMQ, Mailpit y PgAdmin se abren en esta máquina (127.0.0.1 / localhost).

## Correcciones
- `citeck edit` acepta un puerto con dirección (`127.0.0.1:15432:5432`); antes, un puerto así impedía que la aplicación arrancara. Un puerto que el launcher no puede leer se rechaza antes de guardar nada.
- Una edición demasiado grande, o una definición sin imagen, se rechaza en lugar de guardarse recortada.
- Vuelve a ser posible crear bases de datos en clústeres de PostgreSQL inicializados con una imagen antigua (las bloqueaba una diferencia de versión de collation tras una actualización de glibc). El contenedor de postgres se recrea una vez.
- Seguridad: el proxy ya no deja pasar como usuario guest las peticiones anónimas a recursos de Alfresco y Share (`/alfresco/…`, `/share/res/…`). El contenedor del proxy se recrea una vez.
