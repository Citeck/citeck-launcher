## Correções
- Atualizar a partir de um launcher 1.x antigo já não impede o launcher de iniciar: um namespace que guardava os usuários numa única linha "nome:senha" é migrado com esses nomes de usuário.
- A imagem de proxy definida nesse namespace é mantida em vez de se perder.
- Um namespace vindo de um launcher anterior à 1.3.6 mantém o último bundle resolvido, por isso inicia mesmo que esse bundle tenha sido movido depois no repositório de bundles.
