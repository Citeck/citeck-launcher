## Novidades
- Edite a configuração do espaço de trabalho (workspace-v1.yml) diretamente na tela de boas-vindas — uma engrenagem ao lado do seletor de espaço de trabalho abre um editor YAML com marcadores de alterações. Suas edições são salvas como um delta sobre a referência do git e reaplicadas a cada sincronização.
- Escolha exatamente quais volumes incluir ao criar um snapshot.

## Correções
- Os status no app de desktop agora atualizam em tempo real em todas as plataformas; no Windows eles não apareciam mais congelados (por exemplo, todos em "Carregando") durante uma inicialização longa até você pressionar "Parar".
- No Windows, a janela não fica mais sem resposta a cliques após confirmar a senha mestra ou fechar um diálogo aninhado.
- Falhas ao baixar imagens agora registram a causa nos logs (por exemplo, uma conexão recusada ao registro), para deixar claro por que o download não foi concluído.
- A lista de namespaces mostra status localizados, e namespaces excluídos desaparecem da lista de início rápido.
- A tela de boas-vindas é legível no tema claro, e as caixas de seleção nativas são renderizadas corretamente e alinhadas em ambos os temas.
- O cabeçalho do namespace é atualizado de forma confiável após uma renomeação, e escolher um valor em uma lista suspensa não desloca mais o layout.
- O instalador agora inicia o aplicativo automaticamente após uma instalação ou atualização no Windows.
