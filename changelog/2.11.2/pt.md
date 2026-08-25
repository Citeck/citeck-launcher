## Correções
- **A atualização a partir da 1.x agora traz todos os seus workspaces e namespaces.** Apenas uma entrada por página de armazenamento era lida, portanto uma instalação com 19 namespaces migrava um — e informava que a migração tinha corrido bem. Agora são restaurados os workspaces, os namespaces, os repositórios git, o estado das aplicações paradas e os ficheiros alterados de cada namespace. Isto corrige a migração em si; os dados perdidos numa atualização já feita não são recuperados automaticamente — a sua base de dados 1.x original está intacta em `storage.db.kotlin-bak`, fale connosco e ela pode ser importada de novo.
- **macOS: atualizar já não bloqueia o launcher.** Depois de clicar em «Atualizar», a janela ficava em «A atualizar…» com todos os botões desativados, incluindo «Cancelar», e o launcher ficava inutilizável até ser reiniciado. Agora a janela recarrega sozinha assim que a nova versão arranca, «Cancelar» funciona sempre e os botões deixaram de se deslocar quando a atualização começa.

## Atualizações
- Mailpit 1.31.0, pgAdmin 9.17 e ZooKeeper 3.9.5.
