# MOKA PAY — Fonctionnalités

> Document de partage (FR). Liste des acteurs et de leurs capacités. Version
> française de `00-specs.drawio`. La spécification complète (en anglais) se trouve
> dans `design-docs/01-project-spec.md`.

## En bref

MOKA PAY transfère de l'argent depuis la **diaspora** vers des bénéficiaires en
**RDC / Congo**. La plateforme ne détient jamais les fonds : un **client** paie
dans le réseau, un **agent** local vérifié paie le bénéficiaire avec son propre
solde mobile money, et MOKA PAY orchestre l'appariement, conserve la **preuve**
des deux paiements, et règle la **commission** de l'agent. Le **solde de
garantie** de l'agent est la caution qui rend le paiement fiable.

**Trois types d'acteurs** — un même compte (identité unique, connexion par OTP) ;
c'est le **rôle** qui débloque les capacités.

| Acteur | Rôle | Identité |
|---|---|---|
| **Client** (expéditeur) | `USER` | Client de la diaspora qui envoie de l'argent. |
| **Agent** | `AGENT` | Partenaire local vérifié qui paie les bénéficiaires et gagne une commission. |
| **Admin** | `OPS` / `ADMIN` | Équipe interne MOKA PAY : vérifie les agents, valide les mouvements d'argent, gère les paiements. |

Le **bénéficiaire** n'est *pas* un acteur : c'est une donnée du transfert, sans
compte ni connexion.

**Légende**
- texte simple — action déclenchée par l'acteur
- `*` — **passif / déclenché par le système** : se produit *pour* l'acteur (par
  le système ou un autre acteur). Listé pour ne pas l'oublier.
- *(hors plateforme)* — se produit en dehors du système ; l'app ne fait que
  l'enregistrer / en garder la preuve.

Capacités de **compte de base** (tout compte connecté, quel que soit le rôle) :
se connecter / déconnecter, gérer son profil, consulter et gérer ses
notifications. Non répété par acteur ci-dessous.

---

## Client (`USER`)

Le client (ou expéditeur) est l'utilisateur principal de l'application. En tant
que client, je peux :

```
1.1    Créer un compte et me connecter
1.2    Obtenir un devis (taux / frais / montant reçu)
1.3    Initier un transfert
1.4  * Être apparié à un agent
1.5    Téléverser la preuve que j'ai financé le transfert   (paiement hors plateforme)
1.6    Suivre un transfert et voir l'agent assigné
1.7    Confirmer que le transfert a été effectué           (téléverser optionnellement une preuve du bénéficiaire)
1.8    Gérer mon compte (informations de profil)
1.9    Consulter mon historique de transferts
1.10 * Recevoir des notifications
1.11   Ouvrir un litige
1.12   Gérer mes bénéficiaires enregistrés
(ouvert) Vérifier mon identité / KYC                        (décision D6)
```

---

## Agent (`AGENT`)

L'agent est l'utilisateur qui exécute les transactions des **clients**. En tant
qu'agent, je peux :

```
2.1  * Avoir mon compte créé par un admin
2.2    Consulter et traiter mes transactions assignées
2.3  * Être apparié à une transaction                       (disponibilité, garantie, compatibilité
                                                            wallet, taux de réussite, niveau, localisation, charge)
2.4    Vérifier la preuve de financement du client et payer le bénéficiaire   (hors plateforme)
2.5    Téléverser ma preuve d'exécution du transfert
2.6    Gérer mon solde de garantie
       2.6.1    Voir les canaux de dépôt officiels
       2.6.2    Initier un dépôt + téléverser la preuve      (paiement hors plateforme)
       2.6.3  * Faire valider mon dépôt par un admin
       2.6.4    Consulter mon solde et l'historique des mouvements
       2.6.5    Retirer ma garantie                          (validé par un admin ? décision D5)
       2.6.6    Contester un dépôt
2.7    Consulter le résumé de mes commissions
2.8  * Recevoir les paiements de commission
2.9    Gérer mes informations de compte
2.10 * Bâtir ma réputation / mon niveau selon ma performance  (N4 requiert une assurance)
2.11 * Recevoir des notifications
2.12 * Voir ma garantie utilisée comme caution pour chaque transaction
```

---

## Admin (`OPS` / `ADMIN`)

L'admin fait partie de l'équipe interne MOKA PAY qui assure le fonctionnement de
la plateforme. En tant qu'admin, je peux :

```
3.1    Enrôler un utilisateur comme agent
3.2    Gérer les agents
       3.2.1  Lister et consulter tous les agents
       3.2.2  Voir le détail du compte d'un agent
       3.2.3  Définir / ajuster le niveau d'un agent (N1–N4)
3.3    Gérer les demandes de dépôt des agents   (voir / valider / rejeter)
3.4    Gérer les demandes de retrait des agents (voir / valider / rejeter)   (si validé par admin — D5)
3.5    Gérer les commissions hebdomadaires      (calculer / marquer comme payées)
3.6    Consulter toutes les transactions de la plateforme
3.7    Consulter le résumé financier de la plateforme
3.8    Gérer les litiges                        (voir tous — client & agent / résoudre)
3.9    Émettre un remboursement client          (nouveau — absent du prototype)
3.10   Modérer les comptes                      (suspendre / réactiver / bannir)
3.11 * Recevoir les alertes opérationnelles     (mouvements d'argent en attente, alertes AML)
```

---

## Diagrammes de flux (FR)

Dans ce dossier :
- `01-transfer-flow-happy-path.drawio` — flux de transfert (chemin nominal)
- `02-agent-onboarding.drawio` — enrôlement d'un agent

À venir : le flux **litige / transfert en échec** (le plus critique — le seul qui
modifie le solde).
