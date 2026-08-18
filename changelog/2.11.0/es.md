## Novedades
- **La actualización automática ya funciona en macOS y Windows**, no solo en Linux. El launcher descarga la nueva versión, verifica su firma y la instala por sí mismo.

## Correcciones
- **Una actualización interrumpida ya no deja el launcher sin poder arrancar.** Una actualización descargada pero nunca comprobada —porque se cerró la aplicación o falló entre medias— ahora se comprueba en el siguiente arranque y se revierte automáticamente si no levanta.
- **Alfresco y Solr ya no reciben opciones de JVM que su Java 8 no entiende.** Una opción desconocida no se ignora: la JVM directamente no arranca.
- **Alfresco y Solr calculan su memoria a partir del límite del contenedor.** El heap de Solr era exactamente del tamaño de su propio contenedor, lo que acaba con el kernel matándolo; Alfresco no tenía ningún límite. Ahora el límite de Solr es de 2560 MB y el de Alfresco, de 8 GB.
- **Los servicios ya no reservan todo su heap al arrancar.** `heapSize` fija solo el máximo, así que levantar un espacio de nombres grande toma la memoria a medida que hace falta.

## Mejoras
- El registro del demonio ya no repite el cálculo de memoria de la JVM para cada servicio en cada recarga.
- Compilado con Go 1.26.6, que incluye correcciones de seguridad de la biblioteca estándar.

## Actualización
- En el primer arranque tras la actualización, los contenedores Java se recrean una vez.
