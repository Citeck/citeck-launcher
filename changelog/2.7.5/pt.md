## Correções
- Parar um namespace agora remove de forma limpa todos os seus contêineres — um aplicativo de parada lenta (por exemplo, eapps) não fica mais no estado «Exited» após o desligamento, e a rede do namespace é removida de forma confiável.
- Excluir um espaço de trabalho agora recupera tudo o que há nele — os volumes de dados Docker de cada namespace (PostgreSQL, MongoDB, …) e os registros do banco de dados, não apenas os arquivos em disco.
- O servidor de configuração integrado (desktop) agora é executado apenas enquanto um namespace está ativo e libera sua porta ao parar, de modo que reiniciar o aplicativo com um namespace parado não falha mais com «address already in use».
