## Corrections
- **Windows : plus de fenêtre de console à côté du lanceur.** L'application ouvrait une seconde fenêtre noire à chaque démarrage ; elle a disparu, et le journal propre au lanceur est désormais écrit dans `launcher.log`, dans le dossier des journaux.
- **La création du mot de passe maître signale maintenant que les deux saisies diffèrent.** La vérification empêchait déjà de continuer, mais sans rien afficher : le bouton « Confirmer » semblait donc sans effet.

## Améliorations
- Mise à jour de la bibliothèque Git en 5.19.2, qui corrige deux vulnérabilités dans la gestion des dépôts : une traversée de répertoires via des noms de références forgés, et des opérations sur l'arbre de travail qui suivaient les liens symboliques.
