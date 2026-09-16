## Nouveautés
- **Qdrant publie désormais son port gRPC (6334)**, celui par lequel `rag` lui parle réellement ; 6333 n'a jamais porté que le contrôle de santé. Un `rag` lancé hors du launcher (depuis l'IDE) atteint maintenant le magasin vectoriel de ce stand sans aucune configuration : ses valeurs par défaut pointent déjà sur `localhost:6334`.

## Changements
- **Arrêter `rag` ne supprime plus Qdrant.** Le magasin reste dans l'espace de noms comme une application arrêtée avec un bouton de démarrage — exactement ce qu'il faut pour exécuter `rag` soi-même : arrêtez-le ici, démarrez-le là, et les données indexées restent accessibles. Il ne démarre jamais de lui-même : sur tout stand où RAG est simplement désactivé, Qdrant reste arrêté et ne consomme aucune mémoire, comme avant. Démarrez-le délibérément (`citeck start qdrant`) et il tourne jusqu'à ce que vous l'arrêtiez ou redémarriez le launcher.
- **Idem pour `ai` et son sidecar de reconnaissance vocale.** Arrêter `ai` conserve le sidecar décrit et arrêté au lieu de le supprimer : un `ai` lancé depuis l'IDE trouve toujours la reconnaissance vocale sur localhost.
- **L'indicateur RAG de l'assistant suit désormais le fait que l'espace de noms POSSÈDE `rag`**, et non qu'il soit en cours d'exécution. Basculer `rag` ne réécrit plus et ne recrée plus le conteneur `ai`.
