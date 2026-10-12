## Novidades
- Ao criar um namespace, escolha DB ou S3; a disponibilidade de S3 depende do bundle e da edição selecionados.
- A API para agentes e os comandos de descoberta sem iniciar o daemon descrevem as operações suportadas pelo Launcher.
- A recuperação de atualizações Git oferece cache, nova tentativa e escolha de token por repositório, com a última sincronização bem-sucedida.
- Downloads de atualizações e snapshots mostram os bytes recebidos e retomam automaticamente transferências interrompidas.

## Correções
- Downloads ativos continuam enquanto chegam bytes, sem limite de duração total.
- A inicialização normal de S3 não mostra mais um aviso; erros de configuração e conexão permanecem visíveis.
- Mailpit usa as portas do host 17011 para SMTP e 17012 para a interface web.
- Os diagnósticos Git ocultam credenciais incluídas nas URLs dos repositórios.
