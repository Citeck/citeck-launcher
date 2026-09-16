## Novidades
- **Um bundle agora pode exigir uma versão mínima do launcher.** Criar ou editar um namespace agora é recusado se o bundle selecionado exigir uma versão do launcher mais nova do que a instalada — a mensagem indica para qual versão atualizar. Um namespace que já executa esse bundle não é afetado, e escolher "LATEST" sempre seleciona o bundle mais recente que este launcher consegue realmente executar.
- **O ícone de engrenagem das configurações do namespace agora mostra um ponto quando há uma versão de bundle mais recente disponível** no seu repositório de bundles — verde se este launcher consegue executá-la, âmbar se antes for preciso um launcher mais novo. Passe o mouse sobre o ícone para ver a versão e o que fazer.

## Correções
- **O "Force Update" volta a ler de fato o repositório do workspace.** Antes o botão respondia com sucesso sem baixar nada durante uma hora após a sincronização anterior, e uma versão de bundle recém-enviada não aparecia nem na lista de versões nem no indicador "há um bundle mais novo".
- **O botão ↻ ao lado da lista de bundles atualiza o repositório que o launcher realmente lê.** Quando os repositórios de bundles ficam dentro do repositório do workspace — o layout habitual —, ele clonava uma segunda cópia não utilizada por repositório e relatava sucesso embora nada mudasse na tela.
