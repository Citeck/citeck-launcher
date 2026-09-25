## Correções
- Com o Keycloak 26.6.2 ou mais recente, o proxy volta a aceitar tokens de acesso: as requisições com o cabeçalho `Authorization: Bearer` ou o cookie `PA` (o aplicativo móvel, as integrações) eram recusadas, embora o login pelo navegador continuasse funcionando.
- Com o Keycloak 26.5 ou mais recente, as configurações do realm `ecos-app` voltam a ser salvas no console de administração do Keycloak; antes o salvamento falhava com "Client Session Idle Timeout cannot be greater than Realm SSO Idle Timeout".
- A atualização recria o contêiner do Keycloak uma vez, para que a configuração corrigida entre em vigor.
