## Novidades
- **Busca semântica RAG.** Os bundles enterprise agora oferecem uma aplicação `rag` para busca semântica na sua base de conhecimento, desativada por padrão. Ao iniciá-la, um banco de dados vetorial Qdrant é levantado automaticamente e o acesso à base de conhecimento é ativado para o assistente de IA; uma `rag` desativada continua sem consumir memória extra — o Qdrant nem chega a ser criado. Os bundles community não são afetados: nem `rag` nem Qdrant aparecem neles.
- **Dependências de webapp configuráveis.** O `dependsOn` de uma webapp agora pode ser estendido via `webapps[].defaultProps.dependsOn` do workspace e `webapps.<id>.dependsOn` de um namespace — as dependências configuradas se somam às incorporadas, sem substituí-las. Um ciclo em `dependsOn` agora faz a geração falhar com um erro claro, em vez de deixar as aplicações envolvidas esperando para sempre.

## Alterações
- **Uma dependência parada não deixa mais a aplicação dependente iniciar sem ela.** Antes, uma aplicação cuja dependência havia sido parada manualmente iniciava mesmo assim e depois falhava nas verificações de saúde. Agora ela espera, mostrando no status o que está esperando, e prossegue assim que a dependência é iniciada.
- **Uma imagem fixada pelo bundle não pode mais ser sobrescrita pela configuração do workspace ou do namespace.** O bundle agora sempre prevalece; para usar outra imagem em um stand específico, use `citeck edit <app>` explicitamente. Uma configuração que ainda assim tente sobrescrever a imagem agora é registrada como aviso em vez de ser aplicada silenciosamente.

## Correções
- **Alternar alfresco agora regenera o namespace**, como já acontecia com onlyoffice e ai desde a 1.4.1. Antes, após uma parada/início manual, o proxy continuava tratando alfresco como disponível (ou não) contrariando a realidade, até que o próximo reload não relacionado corrigisse isso.
