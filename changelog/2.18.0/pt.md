## Novidades
- Módulos experimentais (beta): uma versão pode entregar novos serviços como beta na seção `experimental:` do bundle. Eles são ativados por ambiente na nova aba «Experimentais» do diálogo de criação/edição; esses aplicativos são marcados como BETA na tabela de aplicativos e no `citeck status`.
- Modo servidor: `citeck features` lista os módulos beta oferecidos pelo bundle do ambiente e `citeck features enable|disable <id>` os liga ou desliga; `citeck install` pergunta quais ativar. Um módulo desligado é removido na próxima aplicação da configuração, e seus volumes de dados são mantidos.
- Um módulo beta que uma versão posterior torna permanente continua rodando onde estava ativado; nos demais ambientes é oferecido como qualquer aplicativo novo, conforme o modelo do workspace.
- O bundle pode fornecer nomes e descrições das funções em vários idiomas.

## Correções
- A exportação e a exclusão de arquivos dos aplicativos funcionam quando o diretório de dados do Launcher é acessado por um link simbólico.
