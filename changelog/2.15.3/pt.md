## Correções
- Uma dependência cujo volume de dados foi excluído (por exemplo, na página de volumes) não é mais oferecida como atualização cuja migração depois falha: sem dados a proteger, a versão do bundle é aplicada na próxima inicialização.
- Quando uma migração exige um launcher mais recente, `citeck deps` agora orienta a executar novamente o `install.sh`; `citeck update` apenas atualiza as definições do workspace e dos bundles.
- O aviso do desktop antes de excluir um namespace agora informa que os volumes de dados e a pasta dele, incluindo os snapshots, também são excluídos.
