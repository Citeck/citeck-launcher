## Novidades
- O launcher avisa quando o diretório de dados do Docker está com pouco espaço em disco, antes que os contêineres comecem a falhar.

## Correções
- "Forçar atualização e iniciar" agora responde instantaneamente e força uma verificação de novas versões de pacotes via Git, reutilizando as imagens de versão já baixadas em vez de baixá-las novamente.
- Aplicativos que travavam ao parar agora se recuperam sozinhos e não congelam mais o namespace.
- O recarregamento de um namespace não trava mais indefinidamente se uma operação do Git emperrar.
- As reinicializações automáticas de contêineres reutilizam a imagem local, portanto nunca mudam de versão silenciosamente nem falham durante uma indisponibilidade do registro.
- Contêineres, volumes e redes do Docker deixados por namespaces excluídos agora são limpos automaticamente na inicialização.
- O namespace ativo sempre mostra seus próprios contêineres, sem divergência entre namespaces.
- Janelas adicionais (logs, editor) agora fecham quando você minimiza o aplicativo para a bandeja ou volta para a tela de boas-vindas.

## Alterações
- Os namespaces são editados pelo formulário; o editor de YAML bruto e o menu de contexto da engrenagem foram removidos.
