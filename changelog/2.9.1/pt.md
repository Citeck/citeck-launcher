## Novidades
- **Página de configurações.** O ícone de engrenagem agora abre uma página de configurações de verdade, com os ajustes do repositório Git do espaço de trabalho e as credenciais dos registros Docker privados — antes acessíveis apenas por um menu ao passar o cursor ou por um diálogo de erro.

## Correções
- **macOS: "citeck-launcher is damaged and can't be opened".** As compilações baixadas não tinham assinatura, e o Gatekeeper relata isso com essa mensagem enganosa, sem como contornar. O pacote volta a ser assinado, então no primeiro início aparece o aviso comum de desenvolvedor não identificado — abra uma vez com clique direito → "Abrir".
- **Atualizar a partir da 1.x não perde mais o seu espaço de trabalho.** Quando o banco de dados antigo não podia ser lido, o launcher o substituía silenciosamente por um vazio. A URL e o branch do repositório agora são recuperados do clone existente, e uma migração que não conseguiu trazer tudo avisa, em vez de parecer uma instalação nova.
- **Uma falha de rede não troca mais um repositório de pacotes.** Um breve tempo limite durante um pull do Git podia substituir um repositório pelo conteúdo de outro. Erros de rede temporários agora preservam a cópia existente, e uma reclonagem é recusada se fosse instalar um repositório diferente.
- **As credenciais de registro podem ser corrigidas.** Escolher o segredo errado na primeira vez não deixava caminho de volta. As associações agora aparecem nas configurações, podem ser reatribuídas ou removidas, e uma credencial de outro host é sinalizada em vez de parecer válida.
- **Erro mais claro para um repositório de pacotes desconhecido.** Em vez de servir silenciosamente o espaço de trabalho padrão da Citeck, o launcher nomeia o repositório, os identificadores declarados e onde corrigi-los.
- **O ícone de engrenagem não desaparece mais.** Clicar nele sem um namespace aberto voltava à tela de boas-vindas e escondia o ícone.
