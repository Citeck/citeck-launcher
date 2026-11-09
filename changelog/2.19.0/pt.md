## Novidades

- Conecte agentes de IA locais via MCP para consultar o estado e os logs; a função Developer também permite gerenciar aplicativos, editar parâmetros de execução e configurações do Spring, e coletar diagnósticos da JVM.
- Gerencie o acesso dos agentes nas configurações do aplicativo desktop: aprove conexões OAuth ou crie tokens manualmente, escolha Observer ou Developer, defina a validade e revogue o acesso. O MCP fica desativado por padrão e, quando ativado, escuta apenas na interface de loopback.
- Defina limites de CPU por aplicativo em núcleos, incluindo valores fracionários, pelo editor de configuração do aplicativo.

## Correções

- O recarregamento agora informa falhas ao gravar arquivos de configuração gerados, em vez de continuar com arquivos não salvos.
- Arquivos de configuração editados não são mais substituídos silenciosamente por conteúdo vazio quando o arquivo atual está indisponível e as alterações salvas não podem ser reaplicadas.
- Edições de configuração YAML preservam todos os documentos de arquivos com vários documentos YAML.
- Comandos de diagnóstico da JVM não são mais repetidos automaticamente quando já podem ter sido enviados à JVM.
