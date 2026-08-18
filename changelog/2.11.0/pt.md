## Novidades
- **A atualização automática agora funciona no macOS e no Windows**, não apenas no Linux. O launcher baixa a nova versão, verifica a assinatura e a instala sozinho.

## Correções
- **Uma atualização interrompida não deixa mais o launcher sem conseguir iniciar.** Uma atualização baixada mas nunca verificada — porque o aplicativo foi fechado ou caiu no meio do caminho — passa a ser verificada na inicialização seguinte e revertida automaticamente se não subir.
- **Alfresco e Solr não recebem mais opções de JVM que o Java 8 deles não entende.** Uma opção desconhecida não é ignorada: a JVM simplesmente não inicia.
- **Alfresco e Solr dimensionam a memória a partir do limite do contêiner.** O heap do Solr tinha exatamente o tamanho do próprio contêiner, o que termina com o kernel encerrando o processo; o Alfresco não tinha limite nenhum. Agora o limite do Solr é de 2560 MB e o do Alfresco, de 8 GB.
- **Os serviços não reservam mais todo o heap na inicialização.** `heapSize` define apenas o máximo, então subir um namespace grande consome memória conforme a necessidade.

## Melhorias
- O log do daemon não repete mais o cálculo de memória da JVM para cada serviço a cada recarga.
- Compilado com Go 1.26.6, que traz correções de segurança da biblioteca padrão.

## Atualização
- Na primeira inicialização após a atualização, os contêineres Java são recriados uma vez.
