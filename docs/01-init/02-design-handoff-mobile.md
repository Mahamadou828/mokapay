# 02 — Design Handoff: Client & Agent mobile

> **For:** Claude Design
> **Brief:** design the **Client (expéditeur)** and **Agent** journeys of MOKA PAY.
> **Mobile only** — the app is mobile-first, there is no desktop layout.
> **Language:** the entire UI is in **French**.
>
> ### This is a fresh start
> An earlier iteration exists in `design-reference/`. **Do not continue, extend or
> match those files.** Design this from scratch. That folder is useful only as
> *product* context (which screens exist, roughly what data appears) — treat its
> layouts and components as discarded.
>
> The one thing we keep is the **brand direction**: deep green, modern, clean
> (§2). Everything else — layout, components, navigation patterns, spacing — is
> yours to define.

---

## 1. What MOKA PAY is

MOKA PAY moves money from the **diaspora** (Europe) to recipients in the
**DRC / Congo** via mobile money. The platform **never holds the funds**:

1. A **client** creates a transfer and is matched to an **agent**.
2. The client pays the agent **off-platform** and uploads proof.
3. The agent pays the recipient **off-platform** — from their own mobile-money
   float — and uploads proof.
4. The client confirms receipt; the agent's **commission** is settled weekly.

The agent posts a **guarantee balance** that is held as collateral during each
transfer. That collateral is what makes handing a payout to a human agent safe —
so anything touching it must feel trustworthy and legible.

**Actors:**

| Actor | In this handoff? |
|---|---|
| **Client / Expéditeur** (`USER`) | ✅ |
| **Agent** (`AGENT`) | ✅ |
| **Admin / OPS** (back-office) | ❌ later, and desktop |
| **Recipient / Bénéficiaire** | ❌ no screens — they never log in |

**The emotional core:** people are sending money home to family, through a
stranger, with real money at risk on both sides. The design should feel
**calm, transparent and trustworthy** — never playful, never opaque about fees or
status.

---

## 2. Brand direction (the one thing carried over)

Deep green, modern, sober. Clean and information-dense without feeling like a
spreadsheet. No gradients, no skeuomorphism, no drop-shadow-heavy "fintech neon".

**Palette**
| Role | Hex |
|---|---|
| Primary green — brand, CTA, dark surfaces | `#103D2C` |
| Accent green — positive status, active states | `#3ECF8E` |
| Tinted surface — badges, icon chips | `#EAF1ED` |
| Muted text on dark green | `#9fc3b3` |
| Text primary / secondary / tertiary | `#151515` / `#767676` / `#9a9a9a` |
| Borders | `#ECECEA` / `#F0F0EE` |
| Neutral fills | `#F4F6F5` / `#FAFBFA` |
| Dark context (disputes, alerts) | `#151515` |
| Amber — "needs checking" | `#d8a23a` |

You may add **one** additional semantic color if you need a true error red — the
palette currently has no red, and rejected/failed states need one.

**Typography** — currently implemented in code, so changing them has a cost, but
propose an alternative if it genuinely serves the design:
- **Schibsted Grotesk** — titles, numbers, amounts (700–800)
- **Hanken Grotesk** — body, labels, lists (400–700)
- All amounts, phone numbers and references: **tabular figures**

**Frame:** 390 × 812 mobile.

**Principles (what "modern" means here):**
- One clear **primary action** per screen.
- **Money is the hero** — amounts, fees and rates get the strongest hierarchy, and
  fees are never hidden or shown late.
- **Status must be readable at a glance** — the user should instantly know
  *"is it on me, or am I waiting?"*
- Generous whitespace; calm density. Big tap targets.
- Plain French, no jargon. Never surface internal status codes to users.

---

## 3. Screens — Client (Expéditeur)

### Onboarding
| # | Screen | Notes |
|---|---|---|
| C1 | Entry / choose role | « Je suis un utilisateur » / « Je suis un agent » |
| C2 | Login — phone number | country dial code + phone. **No password anywhere in this app** |
| C3 | SMS code (OTP) | 6 digits, auto-advance, resend countdown |
| C4 | Create account | first name, last name, language (fr/en). Signup needs **no email, no password, no ID** |
| C5 | Identity verification (KYC) | *optional / on hold* — design it, but it's gated on an open decision |

### Sending money — the core flow
| # | Screen | Notes |
|---|---|---|
| C6 | Home | entry to a new transfer, recent transfers, today's rate. Note: **clients have no wallet or balance** — don't design one |
| C7 | Quote / simulate | amount + **rate + fees + what the recipient receives**, before committing. Clients will check the rate casually, without sending |
| C8 | New transfer | amount, pick a **saved beneficiary** or enter a new one, choose the recipient's mobile-money operator |
| C9 | Review | transparent breakdown: sent · fee · rate · received. Confirming starts the transfer |
| C10 | Searching for an agent | a **waiting state** with no user action. Must feel alive, and degrade gracefully when it takes a while |
| C11 | Agent assigned | the agent's identity/rating **plus the payment details the client must now pay to** |
| C12 | Upload funding proof | after paying off-platform — screenshot upload |
| C13 | Tracking | timeline of the transfer through its statuses (§5) |
| C14 | Confirm receipt | confirm the recipient was paid; **optionally** attach a recipient proof |

### Managing
| # | Screen | Notes |
|---|---|---|
| C15 | Transfer detail | full breakdown, proofs, timeline, entry to open a dispute |
| C16 | History | list + filter by status |
| C17 | Saved beneficiaries | list / add / edit / delete |
| C18 | Notifications | list, unread state, mark read |
| C19 | Profile & settings | profile info, language, security |
| C20 | Dispute — open & follow | open a dispute on a transfer, then follow it to resolution |

---

## 4. Screens — Agent

The agent app is a **work tool**: money in, jobs to do, money out. It should feel
efficient and dashboard-like, still within the same brand.

### Enrollment
An agent never signs themselves up — an admin invites them.

| # | Screen | Notes |
|---|---|---|
| G1 | Invitation + terms | "an admin invited you to become an agent" → read & accept the terms. **Must be refusable** |
| G2 | Fund your guarantee (20 USD) | shows the **official deposit channels** (M-Pesa, Orange Money, Airtel Money, Afrimoney, Rawbank, Equity BCDC) with account references and a **copyable reference** `DEP-2026-01234` |
| G3 | Upload deposit proof | amount, channel used, sender details, proof image |
| G4 | Awaiting validation | pending state **+ a visible "something's wrong / get help" path** — admins can be abusive or mistaken, and the agent must have recourse |
| G5 | Activated | agent is live: level N1, balance 20 |

### Doing the work
| # | Screen | Notes |
|---|---|---|
| G6 | Dashboard | **solde / bloqué / disponible**, active transfers, commission this week, level |
| G7 | **Transfer offer — accept or decline** | ⭐ *the most important screen in the agent app.* Amount, commission earned, recipient's operator, **how much guarantee gets blocked**, and a **countdown** before the offer passes to another agent |
| G8 | My transfers | the working queue |
| G9 | Transfer detail & execution | review the **client's funding proof**, see recipient payout details, mark "I've paid" |
| G10 | Upload execution proof | screenshot of the mobile-money payout |

### Money
| # | Screen | Notes |
|---|---|---|
| G11 | Balance & movements | history of every balance movement: type, amount, status, date. Must make **held vs available** unmistakable |
| G12 | Add funds | same shape as G2/G3, any amount, agent-initiated |
| G13 | Withdraw | request a withdrawal → **awaits admin validation, not instant**. Only *available* funds, above a minimum floor |
| G14 | Commissions & payouts | pending this week, total earned, **next payout (Tuesdays)**, history |

### Profile
| # | Screen | Notes |
|---|---|---|
| G15 | My wallets | the mobile-money accounts the agent pays out from |
| G16 | Profile, level & reputation | level N1–N4, success rate, stats. **N4 requires insurance** |
| G17 | Notifications | same pattern as C18 |
| G18 | Dispute / blocked transfer | agent-side dispute |

---

## 5. Status labels (French UI copy)

Internal status codes must **never** appear in the UI. Design with these labels,
and give each a visual treatment (color + icon).

**Transfer**
| Internal | French label | Tone |
|---|---|---|
| `pending_match` | Recherche d'un agent | in progress |
| `pending_agent_approval` | En attente de l'agent | in progress |
| `pending_client_transfer` | En attente de votre paiement | **action required** |
| `pending_agent_transfer` | L'agent effectue le transfert | in progress |
| `pending_client_confirmation` | À confirmer | **action required** |
| `done` | Terminé | success |
| `disputed` | Litige en cours | alert / dark |
| `refunded` | Remboursé | neutral-positive |
| `cancelled` | Annulé | muted |
| `expired` | Expiré | muted |

**Balance movement**
| Internal | French label |
|---|---|
| `INITIATED` | Initié |
| `PENDING_AGENT_ACTION` | En attente de votre paiement |
| `PENDING_ADMIN_APPROVAL` | En attente de validation |
| `DONE` | Validé |
| `REJECTED` | Rejeté |
| `DISPUTED` | Litige |

**Balance vocabulary:** `Solde` (total) · `Bloqué` (held) · `Disponible` (available).

---

## 6. Domain facts for realistic content

- **Currencies:** client pays **EUR**, recipient receives **CDF**. Indicative rate
  **1 € ≈ 2 850 CDF**.
- **Fees:** ~**2 %** of the amount, plus a small FX margin.
- **Agent commission:** **60 % of the fee**, paid out **weekly on Tuesdays**.
- **Mobile-money operators:** Orange Money, M-Pesa, Airtel Money (+ Afrimoney;
  banks Rawbank, Equity BCDC for deposits).
- **Guarantee:** 20 USD minimum to activate. Levels **N1–N4**.
- **References:** transfer `MKP-2026-123456`, deposit `DEP-2026-01234`.
- **Number formatting (French):** `1 250,00 €`, `3 562 500 CDF` — space as
  thousands separator, comma as decimal.

---

## 7. States to cover

For every list and flow screen:
- **Empty** — no transfers, no beneficiaries, no notifications
- **Loading / in progress** — especially C10 "searching for an agent"
- **Action required** — the single most important state in this app; the user must
  instantly see it's on them
- **Error / rejected** — deposit rejected, proof refused (needs that error color)
- **Dispute** — dark treatment
- **Success**

---

## 8. Out of scope

- Admin / back-office (later, desktop)
- Recipient screens — recipients have no account
- Desktop / tablet layouts
- Dispute **resolution** UI (admin-side)

---

## 9. Deliverable

- Mobile frames **390 px** wide on a zoomable canvas.
- Each frame labelled with its code (`C7`, `G7`, …) and a short French title.
- Grouped by journey: **Client** first, then **Agent**.
- French copy throughout; placeholder names/photos fine.
- A short **style sheet frame**: final palette (incl. your error color), type
  scale, buttons, inputs, cards, status chips — so the build has one reference.

**Background reading (context, not layout):**
- `design-docs/01-project-spec.md` — actors, capabilities, balance model, transfer lifecycle
- `design-docs/01-project-specs/*.drawio` — flow diagrams
- `design-docs/fr/` — French flow diagrams (PDF)
- `design-reference/` — earlier iteration, **product context only, do not match**
