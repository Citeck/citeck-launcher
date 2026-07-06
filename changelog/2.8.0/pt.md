## Novidades
- **Links personalizados na barra lateral.** Declare seus próprios links rápidos na configuração do workspace (`workspace-v1.yml` → `links:`), cada um com uma lista opcional de dependências `dependsOn`. Um link fica oculto enquanto uma dependência não faz parte do namespace, desabilitado enquanto uma dependência não está em execução e habilitado nos demais casos. Os links personalizados aparecem na parte inferior da barra lateral.
- **Editar a configuração de um aplicativo pela linha de comando.** `citeck edit <app>` abre a configuração efetiva de um aplicativo no seu `$EDITOR` e a salva como uma substituição por aplicativo (como `kubectl edit`). `--file <caminho>` edita, em vez disso, um arquivo de configuração montado como `application-launcher.yml`, `--list-files` lista os arquivos editáveis, `--reset` restaura o padrão gerado e `--from <arquivo|->` define o conteúdo sem abrir o editor.

## Alterações
- **Os arquivos de configuração montados editados agora chegam ao aplicativo em execução.** Salvar uma alteração em um arquivo de configuração montado (por exemplo `application-launcher.yml`) agora recria o contêiner afetado para aplicar o novo conteúdo; antes a edição era salva, mas só entrava em vigor na próxima alteração não relacionada. A atualização recria uma vez os contêineres dos aplicativos web (o hash de implantação muda) — nenhuma ação necessária.
- O limite de memória do Keycloak foi aumentado de 1 GB para 1,5 GB.
- Os limites de memória agora aceitam unidades fracionárias (por exemplo `1.5g`).
