## Corrections
- La mise à niveau depuis un ancien lanceur 1.x n'empêche plus le lanceur de démarrer : un espace de noms qui enregistrait ses utilisateurs sur une seule ligne « nom:mot de passe » est migré avec ces noms d'utilisateur.
- L'image de proxy définie dans un tel espace de noms est reprise au lieu d'être perdue.
- Un espace de noms issu d'un lanceur antérieur à 1.3.6 conserve son dernier bundle résolu ; il démarre donc même si ce bundle a depuis été déplacé dans le dépôt de bundles.
