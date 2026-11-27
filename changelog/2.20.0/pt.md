## Novidades

- Configurações de serviços acompanhantes para webapps (`webapps[].companions`): os workspaces podem fornecer variáveis de ambiente e dependências de inicialização às webapps somente enquanto o serviço acompanhante (como um gateway LLM opcional) estiver presente no ambiente, preservando a prioridade das configurações do namespace.
- Bancos de dados no PostgreSQL principal do ambiente para serviços adicionais (`additionalApps[].postgresDatabases`): serviços personalizados podem solicitar bancos no cluster PostgreSQL principal para participar automaticamente de migrações, snapshots e pgAdmin sem recriar o PostgreSQL ao alternar recursos.
- Dicas localizadas para links personalizados da barra lateral (`links[].descriptions`): links de workspace agora suportam descrições por idioma com fallback retrocompatível, e a interface as atualiza imediatamente ao trocar de idioma.
- Adicionado o ícone do LiteLLM na barra lateral e atualizado o ícone do Mailpit.
