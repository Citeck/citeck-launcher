## Correções
- **A atualização a partir da 1.x já não faz voltar os namespaces que tinha apagado.** Apareciam na lista como identificadores soltos, sem nome nem bundle, e ao abri-los surgia `namespace "..." not found`. Apagar um namespace na 1.x deixava parte do seu estado para trás, e a migração transformava cada resto numa entrada própria. Se já estiverem na sua lista, remova-os com o ícone do caixote — não afeta mais nada.
- Os namespaces do antigo workspace predefinido mantêm depois da migração o estado das aplicações paradas e o bundle memorizado.
