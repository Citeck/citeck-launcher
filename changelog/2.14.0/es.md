## Novedades
- **Qdrant ahora publica su puerto gRPC (6334)**, el puerto por el que `rag` habla realmente con él; en 6333 solo estuvo nunca la comprobación de estado. Un `rag` ejecutado fuera del lanzador (desde el IDE) llega ahora al almacén vectorial de este entorno sin ninguna configuración: sus valores por defecto ya apuntan a `localhost:6334`.

## Cambios
- **Detener `rag` ya no elimina Qdrant.** El almacén permanece en el namespace como una aplicación detenida con botón de inicio, que es justo lo que hace falta para ejecutar `rag` por su cuenta: deténgalo aquí, inícielo allí y los datos indexados siguen accesibles. Nunca se inicia solo: en cualquier entorno donde RAG esté simplemente desactivado, Qdrant permanece detenido y no consume memoria, igual que antes. Inícielo a propósito (`citeck start qdrant`) y seguirá en marcha hasta que lo detenga o reinicie el lanzador.
- **Lo mismo para `ai` y su sidecar de reconocimiento de voz.** Detener `ai` mantiene el sidecar descrito y detenido en lugar de eliminarlo, de modo que un `ai` ejecutado desde el IDE sigue teniendo reconocimiento de voz en localhost.
- **La marca RAG del asistente depende ahora de SI el namespace tiene `rag`**, no de si `rag` está en ejecución. Activar o detener `rag` ya no reescribe ni recrea el contenedor `ai`.
