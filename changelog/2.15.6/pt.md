## Correções
- Uma imagem de um caminho do registro que o workspace declara sem autenticação (por exemplo, um projeto público do Harbor) agora é baixada de forma anônima e não pede mais credenciais, mesmo que o mesmo registro também hospede imagens privadas.
- As credenciais do registro não se perdem mais quando o workspace lista um caminho público do mesmo registro depois de um privado.
