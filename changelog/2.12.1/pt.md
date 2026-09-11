## Novidades
- **Um bundle ou uma configuração de workspace pode declarar as imagens dos componentes de terceiros numa nova secção `dependencies:`.** Lançadores anteriores a 2.12 não leem essa secção, por isso subir a versão do PostgreSQL, do RabbitMQ ou de outra infraestrutura ali só chega aos lançadores que sabem proteger os dados existentes — os restantes continuam a correr o que correm hoje. Na configuração do workspace, a secção sobe ainda a versão para todos os namespaces desse workspace de uma só vez.

## Correções
- **Uma atualização do ambiente de trabalho já não é revertida só porque o Docker estava lento.** O daemon passa a responder assim que o seu processo está vivo, e não no fim do arranque, pelo que a verificação de integridade avalia a nova versão e não o tempo que a máquina levou a arrancar. Reportado no Windows: uma versão perfeitamente boa foi instalada, declarada «não arranca» ao fim de 60 segundos à espera de um Docker Desktop bloqueado, e revertida.
- **Uma atualização falhada pode ser instalada de novo.** Uma versão que falhasse uma vez era recusada para sempre; a janela de atualização passa a oferecer **Tentar novamente**, continuando o lançador a nunca repetir a tentativa por iniciativa própria.
- A janela de atualização já não afirma que está na versão mais recente ao lado de uma atualização falhada, nem propõe reiniciar para «terminar de instalar» uma atualização revertida.
- `citeck reload` num namespace parado deixa de esperar indefinidamente: recarrega a configuração e diz que esta será aplicada no próximo arranque. Nenhuma espera por um namespace ou pelo daemon pode ficar pendurada para sempre.

## Alterações
- O pgAdmin passa a escolher a imagem como qualquer outra aplicação de terceiros. Numa instalação de ambiente de trabalho em que tanto a configuração do workspace como o bundle nomeiam uma imagem do pgAdmin, ganha a do bundle e o contentor é recriado uma vez.
- Uma atualização falhada passa a indicar o motivo no registo, o daemon anota no arranque que versão está a executar, e o despejo do sistema inclui o registo do próprio lançador e o manifesto de atualizações.
