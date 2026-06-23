## Correções
- Corrigida a tela "Docker não está disponível" que aparecia no macOS mesmo com o Docker Desktop em execução. Agora o launcher detecta o Docker da mesma forma que a CLI do `docker` — pelo contexto Docker ativo (Docker Desktop, colima, Rancher Desktop) — e, quando necessário, recorre a outros caminhos de socket comuns em vez de verificar apenas o caminho padrão.
