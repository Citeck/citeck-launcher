## Novidades
- Aplique zoom na interface com os controles de zoom nativos do webview.
- Reinicie um único aplicativo a partir de um menu de contexto, disponível em qualquer estado do aplicativo.

## Correções
- A instalação no servidor com dados de demonstração agora importa de fato o snapshot de demonstração — antes o namespace era criado, mas iniciava com dados vazios.
- A instalação no servidor agora salva o login do registro privado informado durante a configuração, em vez de descartá-lo silenciosamente.
- Maior paridade com o launcher 1.x para configurações migradas ou editadas à mão: usuários de namespace padrão (admin + fet), ajustes de bundle/workspace e valores padrão de probe/log-startup.
- Agora é solicitado definir uma senha mestra antes de adicionar seu primeiro segredo, em vez de exibir um erro.
- O Quick Start fixa a versão mais recente em uma release concreta, atualiza as versões disponíveis após desbloquear os segredos e abre o painel do namespace assim que inicia.
- Repositórios de workspace personalizados se recuperam automaticamente após você adicionar ou corrigir o token de acesso — sem necessidade de reiniciar.
- As credenciais de registro salvas são vinculadas imediatamente ao seu host de registro.
- Os valores dos segredos são claramente rotulados como Token ou Password.
- Diálogo de snapshots: um único cabeçalho compartilhado e uma atualização automática ao concluir uma exportação ou importação.
- Painel de detalhes do aplicativo com maior contraste; o editor de configuração agora segue o tema do aplicativo.
- Texto de log mais legível e uma dica explicando quando "Excluir tudo" está desativado.
- Excluir um workspace agora também remove os vínculos de credenciais de registro dele.
