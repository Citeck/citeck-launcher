## Corrections
- Avec Keycloak 26.6.2 ou plus récent, le proxy accepte de nouveau les jetons d'accès : les requêtes avec l'en-tête `Authorization: Bearer` ou le cookie `PA` (l'application mobile, les intégrations) étaient refusées, alors que la connexion par le navigateur fonctionnait.
- Avec Keycloak 26.5 ou plus récent, les paramètres du realm `ecos-app` s'enregistrent de nouveau dans la console d'administration de Keycloak ; l'enregistrement échouait avec « Client Session Idle Timeout cannot be greater than Realm SSO Idle Timeout ».
- La mise à jour recrée une fois le conteneur Keycloak, pour que la configuration corrigée prenne effet.
