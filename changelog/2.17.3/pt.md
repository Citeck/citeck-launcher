## Correções
- A importação e a exportação de snapshots mostram o progresso e recuperam o resultado após uma interrupção da conexão, sem deixar a tela de carregamento travada.
- A migração do PostgreSQL após restaurar um snapshot reconhece os diretórios de dados antigos e baixa automaticamente a imagem de origem ausente.
- Desbloquear os segredos atualiza os namespaces parados, disponibilizando S3 e aplicativos que usam segredos sem outra reinicialização.
- Os avisos de inicialização permanecem ocultos até concluir a senha mestra e a atualização seguinte.
- Migrações e reversões de dependências concluídas não deixam mais a última etapa girando nem os controles do namespace bloqueados.
- Os eventos do aplicativo de desktop mantêm a ordem; a sincronização normal não gera mais notificações repetidas de “Conexão restabelecida”.
- As ferramentas de administração do Alfresco Share voltam a funcionar em ambientes com Alfresco.
- A verificação HTTPS rejeita certificados intermediários comprometidos conhecidos.
