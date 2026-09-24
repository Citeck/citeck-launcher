## Alterações
- **Portas publicadas sem endereço agora escutam apenas em 127.0.0.1.** Antes, uma porta assim ficava aberta em todas as interfaces, e o Docker ignora o firewall do host. Apenas o proxy é publicado em todas as interfaces (sempre no modo servidor; no desktop quando o host do namespace não é local). Para abrir uma porta para a rede, indique você mesmo o endereço: `'*:15432:5432'` para todas as interfaces, ou um endereço específico. Um contêiner em execução mantém a ligação antiga até ser recriado.
- Uma edição de `ports` do proxy salva por uma versão anterior não indica endereço e, por isso, agora também significa 127.0.0.1 — verifique-a com `citeck edit proxy`.
- Os links para RabbitMQ, Mailpit e PgAdmin abrem nesta máquina (127.0.0.1 / localhost).

## Correções
- `citeck edit` aceita uma porta com endereço (`127.0.0.1:15432:5432`); antes, uma porta assim impedia a aplicação de iniciar. Uma porta que o launcher não consegue ler é recusada antes de qualquer coisa ser salva.
- Uma edição grande demais, ou uma definição sem imagem, é recusada em vez de ser salva truncada.
- Voltou a ser possível criar bancos de dados em clusters PostgreSQL inicializados por uma imagem mais antiga (uma diferença de versão de collation após uma atualização da glibc os bloqueava). O contêiner do postgres é recriado uma vez.
- Segurança: o proxy não deixa mais passar, como usuário guest, requisições anônimas a recursos do Alfresco e do Share (`/alfresco/…`, `/share/res/…`). O contêiner do proxy é recriado uma vez.
