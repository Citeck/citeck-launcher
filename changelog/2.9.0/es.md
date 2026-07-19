## Novedades
- **HTTPS para un espacio de nombres (autofirmado).** Una nueva opción «Habilitar HTTPS (autofirmado)» en el diálogo del espacio de nombres sirve el proxy por HTTPS con un certificado generado automáticamente. El puerto sigue el esquema automáticamente — 443 para HTTPS, 80 para HTTP — así que activar y desactivar HTTPS simplemente funciona. El navegador muestra una advertencia única por el certificado autofirmado.

## Cambios
- **Las ediciones de configuración son quirúrgicas e instantáneas.** Editar los ajustes de un espacio de nombres o de una aplicación ahora recrea solo los contenedores cuya configuración cambió realmente, y refleja el cambio de inmediato — ya no se vuelven a descargar imágenes de servicios no relacionados. Para obtener las imágenes más recientes, usa «Actualizar e iniciar».
- **El arranque espera la contraseña maestra.** Cuando un espacio de nombres descarga desde un registro privado y tus secretos están bloqueados, ahora espera a que introduzcas la contraseña maestra antes de iniciar — de modo que el diálogo de credenciales ya no oculta la pantalla de desbloqueo.
