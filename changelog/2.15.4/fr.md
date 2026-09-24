## Corrections
- Si le conteneur d’une application ne s’arrête pas dans son délai d’arrêt (par exemple, un service Java qui ignore le signal d’arrêt), il est désormais supprimé de force et l’application passe à « Arrêté », au lieu de rester en « Échec de l’arrêt » avec des journaux impossibles à ouvrir.
- Une application que vous avez arrêtée vous-même et qui est bloquée en « Échec de l’arrêt » relance désormais l’arrêt d’elle-même et passe à « Arrêté » ; elle n’est pas redémarrée.
