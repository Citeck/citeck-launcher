## Mejoras

- Los agentes MCP con el rol de desarrollador ahora pueden editar la configuración completa del contenedor de una aplicación, incluidas imágenes, comandos, montajes, comprobaciones de estado, dependencias y puertos públicos, con las mismas validaciones que la interfaz.
- La edición de archivos mediante MCP ahora admite los archivos de texto montados de la aplicación, incluidos scripts, configuración del proxy y archivos en subdirectorios, en lugar de limitarse a Spring YAML.

## Correcciones

- Se corrigió un fallo del servidor HTTP/1 causado por una condición de carrera al procesar solicitudes demasiado grandes (Go 1.27.4, CVE-2026-97054).
