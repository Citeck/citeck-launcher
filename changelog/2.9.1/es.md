## Novedades
- **Página de configuración.** El icono de engranaje abre ahora una página de configuración real con los ajustes del repositorio Git del espacio de trabajo y las credenciales de los registros Docker privados — antes solo accesibles desde un menú al pasar el cursor o desde un diálogo de error.

## Correcciones
- **macOS: «citeck-launcher is damaged and can't be opened».** Las compilaciones descargadas no llevaban firma, y Gatekeeper lo comunica con ese mensaje engañoso, sin forma de saltarlo. El paquete vuelve a estar firmado, así que en el primer arranque aparece el aviso habitual de desarrollador no identificado — ábralo una vez con clic derecho → «Abrir».
- **Actualizar desde 1.x ya no pierde su espacio de trabajo.** Cuando la base de datos antigua no se podía leer, el lanzador la sustituía en silencio por un espacio vacío. La URL y la rama del repositorio se recuperan ahora del clon existente, y una migración que no pudo trasladarlo todo lo indica en lugar de parecer una instalación nueva.
- **Un fallo de red ya no intercambia un repositorio de paquetes.** Un breve tiempo de espera durante un pull de Git podía reemplazar un repositorio con el contenido de otro. Los errores de red transitorios conservan ahora la copia existente, y una reclonación se cancela si fuera a instalar un repositorio distinto.
- **Las credenciales de registro se pueden corregir.** Elegir mal el secreto la primera vez no dejaba vuelta atrás. Las asociaciones aparecen ahora en la configuración, se pueden reasignar o eliminar, y una credencial de otro host se marca en vez de parecer válida.
- **Error más claro para un repositorio de paquetes desconocido.** En lugar de servir en silencio el espacio de trabajo Citeck predeterminado, el lanzador nombra el repositorio, los identificadores declarados y dónde corregirlos.
- **El icono de engranaje ya no desaparece.** Pulsarlo sin un espacio de nombres abierto devolvía a la pantalla de bienvenida y ocultaba el icono.
