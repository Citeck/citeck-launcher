## Novidades
- **Diagnóstico da JVM sem JDK na imagem.** `citeck jstack <app>` para um dump de threads, `citeck jmap <app>` para um dump de memória baixado para a sua máquina e `citeck jcmd <app> <comando>` para tudo o mais que a própria JVM relata. Funciona enquanto um serviço inicia ou falha, não apenas quando está em execução.
- **`citeck export ls|get|rm`.** Cada contêiner passa a ter um diretório de saída para artefatos — dumps de memória, dumps de banco, relatórios — e os arquivos são listados, baixados e removidos pela CLI.
- **As aplicações web Citeck gravam um dump de memória em OutOfMemoryError por padrão**, compactado, nesse diretório. Apenas o dump mais recente é mantido, para que um ciclo de quedas não encha o disco.
- **Todos os pools de memória da JVM agora são dimensionados a partir do limite do contêiner**: heap, memória direta, metaspace e cache de código. Antes só o heap era limitado, então um vazamento em qualquer outro pool esgotava o contêiner e o kernel o matava sem erro Java e sem dump. Um heap definido à mão é mantido como está e os demais pools são calculados em torno dele; um contêiner pequeno demais para ser dimensionado com segurança recebe tetos apenas onde eles não podem quebrá-lo.
- **Um dump de threads é capturado antes de um serviço ser reiniciado por falhar na verificação de saúde**, de modo que o motivo fica no arquivo de diagnóstico em vez de desaparecer com o contêiner.

## Correções
- **Um serviço em uma longa pausa do coletor de lixo não é mais reiniciado como morto.** A verificação agora tolera cerca de 60 segundos de falhas contínuas em vez de cerca de 20.
- **O proxy é recriado junto com o gateway.** Depois de uma alteração de configuração que recriava o gateway, o nginx continuava roteando para o endereço antigo e as páginas atrás do gateway retornavam 502 embora tudo parecesse saudável.
- **O `citeck diff` não afirma mais que um reload vai desligar o HTTPS** em namespaces com certificado autoassinado ou Let's Encrypt.
- **Notas de atualização que não carregam agora são reportadas** em vez de exibir uma janela vazia.

## Atualização
- Na primeira inicialização após a atualização, cada contêiner é recriado uma vez: cada serviço ganha o diretório de exportação e as novas configurações da JVM. Em um namespace com 17 serviços isso levou cerca de 5 minutos.
