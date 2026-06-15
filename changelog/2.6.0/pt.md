## Novidades
- Credenciais de registro reutilizáveis. Um acesso a um registro privado que você salvar agora pode ser reutilizado entre namespaces e workspaces em vez de ser informado de novo em cada um. Além disso, antes de iniciar um namespace, o launcher verifica se cada registro privado necessário tem credenciais, de modo que um acesso ausente é detectado logo no início e não trava um download mais tarde.

## Alterações
- O seletor de bundle não tem mais uma entrada "LATEST" separada: a versão mais recente é marcada como "(LATEST)" e um namespace fica sempre fixado em uma versão concreta, então nunca muda para uma versão mais nova sozinho.
