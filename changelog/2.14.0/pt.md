## Novidades
- **O Qdrant agora publica a sua porta gRPC (6334)**, a porta pela qual o `rag` realmente fala com ele; a 6333 sempre teve apenas a verificação de saúde. Um `rag` executado fora do launcher (a partir do IDE) alcança agora o armazenamento vetorial deste ambiente sem qualquer configuração: os seus valores padrão já apontam para `localhost:6334`.

## Alterações
- **Parar o `rag` já não elimina o Qdrant.** O armazenamento permanece no namespace como uma aplicação parada com botão de arranque — exatamente o que é preciso para executar o `rag` por conta própria: pare-o aqui, inicie-o ali, e os dados indexados continuam acessíveis. Nunca arranca sozinho: em qualquer ambiente onde o RAG esteja simplesmente desligado, o Qdrant fica parado e não consome memória, tal como antes. Inicie-o deliberadamente (`citeck start qdrant`) e ele fica ativo até que o pare ou reinicie o launcher.
- **O mesmo para o `ai` e o seu sidecar de reconhecimento de fala.** Parar o `ai` mantém o sidecar descrito e parado em vez de o remover, por isso um `ai` executado a partir do IDE continua a ter reconhecimento de fala em localhost.
- **A flag RAG do assistente passa a seguir SE o namespace tem `rag`**, e não se o `rag` está a correr. Alternar o `rag` já não reescreve nem recria o contentor `ai`.
