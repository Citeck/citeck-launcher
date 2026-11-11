## Melhorias

- Agentes MCP com a função de desenvolvedor agora podem editar toda a configuração do contêiner de um aplicativo, incluindo imagens, comandos, montagens, verificações de estado, dependências e portas públicas, com as mesmas validações da interface.
- A edição de arquivos pelo MCP agora abrange os arquivos de texto montados do aplicativo, incluindo scripts, configuração do proxy e arquivos em subdiretórios, em vez de apenas YAML do Spring.

## Correções

- Corrigida uma falha do servidor HTTP/1 causada por uma condição de corrida ao processar solicitações muito grandes (Go 1.27.4, CVE-2026-97054).
