## Correções
- O serviço de IA (gravação de chamadas / reconhecimento de fala) agora está acessível através do proxy, e a ligação com o sidecar STT é aplicada corretamente.
- Anexar ou desanexar a IA, o sidecar STT ou o OnlyOffice em tempo de execução agora tem efeito imediato — o proxy e os vínculos de IA são atualizados sem recriar o namespace.
- Microsserviços externos executados fora do Docker podem novamente acessar o servidor de configuração na nuvem do desktop (para obter os endereços de RabbitMQ, ZooKeeper e banco de dados); antes ele escutava por engano apenas em loopback.
- O aviso para «inserir as credenciais do registro» agora reaparece de forma confiável quando o download de uma imagem falha com um erro de autenticação, mesmo que a notificação única tenha sido perdida.
- Excluir uma credencial de registro não deixa mais um vínculo órfão «(não encontrado)» para o seu host.
- A janela do editor de configuração do espaço de trabalho está maior e mais legível, e «Redefinir para o git» agora é aplicado com um clique.
- As células de CPU e memória não mantêm mais um destaque preso após uma seleção acidental.
- O instalador do Windows não trava mais em «Cancelando…» após uma instalação ou atualização, o que podia bloquear outros instaladores.
