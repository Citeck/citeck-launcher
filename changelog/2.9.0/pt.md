## Novidades
- **HTTPS para um namespace (autoassinado).** Uma nova opção «Ativar HTTPS (autoassinado)» na caixa de diálogo do namespace serve o proxy por HTTPS com um certificado gerado automaticamente. A porta segue o esquema automaticamente — 443 para HTTPS, 80 para HTTP — por isso ativar e desativar o HTTPS simplesmente funciona. O navegador mostra um aviso único para o certificado autoassinado.

## Alterações
- **As edições de configuração são cirúrgicas e instantâneas.** Editar as definições de um namespace ou de uma aplicação agora recria apenas os contentores cuja configuração realmente mudou, e reflete a alteração de imediato — as imagens de serviços não relacionados deixaram de ser rebaixadas. Para obter as imagens mais recentes, use «Atualizar e iniciar».
- **O arranque espera pela palavra-passe mestra.** Quando um namespace descarrega de um registo privado e os seus segredos estão bloqueados, ele agora espera que introduza a palavra-passe mestra antes de arrancar — para que a caixa de diálogo de credenciais deixe de tapar o ecrã de desbloqueio.
