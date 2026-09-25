## Correcciones
- Con Keycloak 26.6.2 o posterior, el proxy vuelve a aceptar tokens de acceso: las solicitudes con la cabecera `Authorization: Bearer` o la cookie `PA` (la aplicación móvil, las integraciones) se rechazaban, aunque el inicio de sesión en el navegador seguía funcionando.
- Con Keycloak 26.5 o posterior, la configuración del realm `ecos-app` vuelve a guardarse en la consola de administración de Keycloak; antes el guardado fallaba con «Client Session Idle Timeout cannot be greater than Realm SSO Idle Timeout».
- La actualización recrea una vez el contenedor de Keycloak para que la configuración corregida surta efecto.
