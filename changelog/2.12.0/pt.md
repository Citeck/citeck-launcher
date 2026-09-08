## Novidades
- **As versões da infraestrutura passam a estar ligadas aos dados.** Uma atualização do bundle já não pode levar o PostgreSQL, o RabbitMQ, o ZooKeeper, o Keycloak ou o MongoDB para uma versão incompatível com os dados que um namespace já tem. O launcher mantém a versão com que os dados foram criados e comunica a nova — com um aviso e o novo diálogo **Dependências** na aplicação de ambiente de trabalho, e com `citeck deps` no servidor.
- **Migração do PostgreSQL 17 para o 18**, a partir do diálogo «Dependências» ou com `citeck deps upgrade postgres`. O namespace é parado, os dados são exportados, é criado um novo cluster num volume **novo**, os dados são restaurados e verificados e o namespace passa a usá-lo — com progresso em cada passo. Qualquer falha é revertida automaticamente para os dados anteriores.
- O volume de dados anterior **é mantido** após uma migração bem-sucedida (aparece na página «Volumes»); elimine-o quando estiver satisfeito com o resultado.
- Se ainda existir um volume de destino de uma tentativa anterior, o launcher mostra o seu tamanho e versão e só o elimina depois da sua confirmação.

## Alterações
- Os novos namespaces são criados com PostgreSQL 18; os existentes ficam no PostgreSQL 17 até serem migrados.
- As atualizações do RabbitMQ, ZooKeeper, Keycloak e MongoDB são comunicadas mas ainda não executadas — aguardam uma versão do launcher capaz de as migrar.
- Alterar a imagem de uma dependência para uma versão incompatível com os seus dados é recusado, tanto no editor de configuração como no `citeck edit`.
- Iniciar, recarregar ou eliminar um namespace é recusado enquanto decorre um snapshot ou uma migração de dependência, e a mensagem indica qual deles.
