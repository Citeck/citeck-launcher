## Correções
- O Início rápido "com dados de demonstração" agora carrega de fato os dados de demonstração — antes o namespace podia iniciar com bancos de dados vazios.
- O namespace criado pelo Início rápido volta a se chamar "Citeck Default", em vez de usar o rótulo do botão.
- O painel e a lista de namespaces agora mostram a versão real do pacote (por exemplo, 2026.3-RC1) em vez de "LATEST".
- "Verificar atualizações" não mostra mais um erro 404 quando você já está na versão mais recente.
- Corrigidos os erros de autenticação do RabbitMQ após reiniciar o servidor ("não é possível carregar o menu"): o contêiner do RabbitMQ tinha pouca memória para concluir a configuração da sua conta de serviço e obtinha uma nova identidade a cada reinício. Agora ele mantém uma identidade estável e tem memória suficiente.
- O arquivo de diagnóstico (dump-system-info) agora oculta senhas, tokens e outros segredos das variáveis de ambiente dos contêineres, logs e arquivos de configuração.
- A interface web agora está totalmente traduzida para chinês, espanhol, alemão, francês, português e japonês — muitos textos que antes apareciam em inglês agora estão localizados.
- Restaurar um instantâneo de namespace não corrompe mais o banco de dados — a restauração agora parte de um volume limpo em vez de mesclar sobre os dados existentes.
