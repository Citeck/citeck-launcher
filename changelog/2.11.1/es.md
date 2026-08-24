## Correcciones
- **Windows: ya no se abre una ventana de consola junto al lanzador.** La aplicación abría una segunda ventana negra en cada inicio; ha desaparecido, y el registro propio del lanzador se escribe ahora en `launcher.log`, dentro de la carpeta de registros.
- **Al crear la contraseña maestra ahora se indica cuando las dos entradas no coinciden.** La comprobación ya impedía continuar, pero no decía nada, así que el botón «Confirmar» parecía no hacer nada.

## Mejoras
- Se actualizó la biblioteca de Git a la versión 5.19.2, que corrige dos vulnerabilidades en el manejo de repositorios: un salto de directorio mediante nombres de referencia manipulados y operaciones sobre el árbol de trabajo que seguían enlaces simbólicos.
