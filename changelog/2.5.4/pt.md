## Correções
- Os namespaces migrados da versão 1.x não são mais corrompidos na primeira inicialização: foi removida uma reimportação automática de snapshot indesejada que podia danificar o banco de dados. Importar um snapshot agora é sempre uma ação manual.
- Após alternar o namespace ativo, o aplicativo passa a mostrar os contêineres desse namespace — o cabeçalho e os detalhes do aplicativo não se contradizem mais.
- Ao excluir um namespace, agora também são removidos seus volumes, rede e contêineres do Docker, de modo que os dados remanescentes não se acumulam mais.
- O uso de CPU pelos contêineres volta a ser exibido corretamente (ficava preso em 0%).
- O visualizador de logs não congela mais após a reinicialização de um contêiner — ele se reconecta automaticamente.
- O formulário de criação/edição de namespace está mais organizado: repositórios de pacotes sem versões publicadas ficam ocultos e a versão mais recente é selecionada automaticamente ao trocar de repositório.
- O título da janela do aplicativo de desktop agora reflete a versão em execução.
- Os logs do daemon são mantidos em uma única pasta "logs" (como na versão 1.x) em vez de ficarem divididos após uma atualização.
