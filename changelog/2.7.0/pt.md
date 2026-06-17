## Novidades
- Edite a configuração de um app e seus arquivos montados diretamente — mesmo com o namespace parado. Suas edições são salvas como deltas sobre a configuração gerada e reaplicadas a cada regeneração, então novas versões de imagem e bundle continuam chegando enquanto suas alterações permanecem.
- Uma faixa de alterações no editor marca as linhas editadas e adicionadas; clique em um marcador para reverter a linha ao valor gerado.
- Detalhes da imagem: digest (sha256), tamanho e plataforma a partir do painel do app, além de baixar a imagem explicitamente — mesmo uma tag de release. Os apps que usam a imagem são atualizados automaticamente após o download.
- Uso de memória atual e limite de cada app no painel de detalhes.
- Baixe os logs de um app ou do daemon direto para a pasta Downloads, com uma notificação e um botão "Abrir pasta".

## Atualizações
- Proteção mais forte para sua senha mestra (derivação de chave Argon2id); os segredos existentes continuam funcionando.
- "Atualizar e iniciar" agora atualiza os repositórios de bundles antes de iniciar; o seletor de bundle lista todos os repositórios configurados e permite atualizar o selecionado.
- Diálogos de segredos mais claros: sem "Pular" quando há uma senha mestra definida, o diálogo de migração oferece uma redefinição e a criação de senha tem um botão "Cancelar".
- Logs mais legíveis: cores de nível ajustadas para o tema claro e mensagens de erro de configuração em monoespaçado.

## Correções
- Um app desanexado não fica mais preso em "Na fila" após uma atualização forçada.
- Um reinício manual ou aplicar a configuração não infla mais o contador de reinícios; o valor no painel coincide com o selo da tabela.
- O editor não pisca mais com fundo preto ou branco ao abrir.
- Menus de contexto: o primeiro clique que fecha um menu não seleciona mais um app nem desloca a linha, e abrir um menu não altera a altura da linha.
- O botão Parar é exibido enquanto o app atualiza e desabilitado enquanto ele para; o cabeçalho da tabela de apps fica fixo durante a rolagem.
- No desktop, fechar o aplicativo agora encerra de forma confiável o daemon em segundo plano em todas as plataformas.
