## Correções
- **Windows: não abre mais uma janela de console ao lado do lançador.** O aplicativo abria uma segunda janela preta a cada início; ela desapareceu, e o log do próprio lançador passa a ser gravado em `launcher.log`, na pasta de logs.
- **A criação da senha mestra agora avisa quando as duas entradas não coincidem.** A verificação já impedia continuar, mas não dizia nada — por isso o botão «Confirmar» parecia não fazer efeito algum.

## Melhorias
- A biblioteca Git foi atualizada para 5.19.2, corrigindo duas vulnerabilidades no tratamento de repositórios: travessia de diretórios por meio de nomes de referência forjados e operações na árvore de trabalho que seguiam links simbólicos.
