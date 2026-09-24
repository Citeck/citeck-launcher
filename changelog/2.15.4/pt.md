## Correções
- Se o contêiner de uma aplicação não parar dentro do tempo limite de parada (por exemplo, um serviço Java que ignora o sinal de parada), ele agora é removido à força e a aplicação passa a «Parado», em vez de ficar em «Falha ao parar» com logs que não podiam ser abertos.
- Uma aplicação que você mesmo parou e que ficou presa em «Falha ao parar» agora repete a parada sozinha e passa a «Parado»; ela não é iniciada novamente.
