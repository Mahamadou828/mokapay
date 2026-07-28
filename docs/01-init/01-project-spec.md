# 01 — Project Specification

> **Status:** living document. This is the spec for what **we** are building in
> this repo — not a description of the prototype. Where the prototype
> (`tmp/prototype/`, teardown in [`00-init.md`](./00-init.md)) got the *product*
> right, we keep the idea; where it got the *implementation* wrong, we diverge.
>
> Covered so far: actors, per-actor capabilities, agent enrollment, the
> balance/money-movement model, and the transfer lifecycle (happy + dispute).
> Still open: the data-entity field lists (Step "Next"), and the decisions in the
> Open Decisions section.

---

## What MOKA PAY is

MOKA PAY moves money from the **diaspora** to recipients in the **DRC / Congo**.
The platform never custodies the funds: a **sender** pays into the network, a
vetted on-the-ground **agent** pays the recipient out of their own mobile-money
float, and MOKA PAY orchestrates the match, holds the **proof** that both legs
happened, and settles the agent's **commission**. The agent's **guarantee
balance** is the collateral that makes trusting them with a payout safe.

---

## Diagrams

Flow diagrams live in [`01-project-specs/`](./01-project-specs/) (draw.io). We
only diagram the **critical** flows, not every feature.

| Diagram | File | Status |
|---|---|---|
| Actor / capability overview | `00-specs.drawio` | done |
| Transfer flow — happy path | `01-transfer-flow-happy-path.drawio` | done |
| Agent onboarding | `02-agent-onboarding.drawio` | done |
| **Dispute / bad-transfer path** | — | **TODO** (design written up in §Transfer lifecycle; see root `TODO.md`) |

---

## Actors

There are **three actor types**. All three are the same underlying *account*
(one identity, one login); what differs is the **role** they carry and the
capabilities that role unlocks.

| Actor | Role value | One-line identity |
|---|---|---|
| **Sender** (a.k.a. Client) | `USER` | A diaspora customer who sends money home. |
| **Agent** | `AGENT` | A vetted local partner who pays recipients and earns commission. |
| **Admin** (a.k.a. Operator) | `OPS` / `ADMIN` | MOKA PAY staff who vet agents, validate money movements, and run payouts. |

A fourth party — the **recipient** — is *not* an actor. They receive money but
do not hold an account or log in (see §Recipient).

### Account model (shared by all actors)

Every human in the system is one **Account**, identified by **phone number** and
authenticated **passwordless via OTP**. An account carries a **role** that
determines what it can do. An `AGENT` account is a normal account *plus* an
**agent profile** (balance, level, wallets, stats) attached to it.

Decisions taken:
- **One identity, role-based capabilities.** No separate user table per actor;
  role is a first-class enum on the account.
- **Phone number is the identity** — unique, the login handle.
- **One role per account** for v1 (see D1).

### Sender / Client (`USER`)
The diaspora customer; the default role every self-registered account gets.
Self-service signup: phone → OTP → name + language → account (`USER`).
Lifecycle: `PENDING → ACTIVE`, with `SUSPENDED` / `BANNED` compliance states.

### Agent (`AGENT`)
A vetted local partner in the DRC who holds mobile-money float and pays
recipients on the platform's behalf, earning commission per completed transfer.
**Not self-service** — an Admin enrolls them (see §Agent enrollment). The agent
must fund a guarantee balance before being matched. Profile carries: balance,
`level` (N1–N4), supported wallets, performance stats. Must be `ACTIVE` **and**
above the minimum guarantee to receive transfers. Agent-profile status
(`INACTIVE → ACTIVE`, `SUSPENDED`) is distinct from account status.

### Admin / Operator (`OPS` / `ADMIN`)
MOKA PAY back-office staff — the human control on money leaving the guarantee
system. Provisioned internally. `OPS` (daily ops) and `ADMIN` (elevated) are two
tiers of the same actor; treated as one for v1 (see D2). Throughout this spec and
the diagrams, "Admin" = this actor.

### Recipient (not an actor)
The person receiving money in the DRC. **Data on a transfer**
(`recipient_phone`, `recipient_name`), not an account, no login. Interacts only
indirectly — through the agent who pays them and an optional recipient-side
proof. Recorded, not authenticated (see D3).

### Relationship map
```
                 creates / vets / suspends
   Admin (OPS/ADMIN) ──────────────────────────────▶ Agent (AGENT)
        │                                              │  pays out to
        │ validates money movements, runs payouts      ▼
        │                                          Recipient (data only, no login)
        ▼                                              ▲
   Sender (USER) ──── creates transfer ───────────────┘
                       (matched to an Agent)
```

---

## Capabilities per actor

**Legend**
- plain — actor-initiated action
- `*` — **passive / system-driven**: happens *to* the actor (by the system or
  another actor). Listed so it isn't forgotten when modelling.
- *(off-platform)* — happens outside the system; the app only records/evidences
  it.

**Base account** (every authenticated account, any role): log in / out, manage
own profile, view & manage notifications. Not repeated per actor below.

### Client (`USER`)
```
1.1    Create account & log in
1.2    Get a quote (rate / fees / recipient amount)
1.3    Initiate a transfer
1.4  * Be matched with an agent
1.5    Upload proof I funded the transfer            (off-platform payment)
1.6    Track a transfer & see the assigned agent
1.7    Confirm the transfer was carried out          (optionally upload recipient proof)
1.8    Manage my account (profile info)
1.9    See my transfer history
1.10 * Receive notifications
1.11   Open a dispute
1.12   Manage saved beneficiaries
(open) Verify identity / KYC                          (see D6)
```

### Agent (`AGENT`)
```
2.1  * Have my account created by an admin
2.2    View & work my assigned transactions
2.3  * Be matched with a transaction                  (availability, guarantee, wallet fit,
                                                       success rate, level, location, load)
2.4    Review the client's funding proof and pay the recipient   (off-platform)
2.5    Upload my proof I carried out the transfer
2.6    Manage my guarantee balance
       2.6.1    See official deposit channels
       2.6.2    Initiate a deposit + upload proof     (off-platform payment)
       2.6.3  * Have an admin validate my deposit
       2.6.4    View my balance & movement history
       2.6.5    Withdraw my guarantee                 (OPS-gated? see D5)
       2.6.6    Dispute a deposit
2.7    View my commission summary
2.8  * Receive commission payouts
2.9    Manage my account information
2.10 * Build reputation / level from performance      (N4 requires insurance)
2.11 * Receive notifications
2.12 * Have my guarantee used as collateral for every transaction
```

### Admin (`OPS` / `ADMIN`)
```
3.1    Enroll a user as an agent
3.2    Manage agents
       3.2.1  List and view all agents
       3.2.2  See an agent's account detail
       3.2.3  Set / adjust an agent's level (N1–N4)
3.3    Manage agent deposit requests   (see / validate / reject)
3.4    Manage agent withdrawal requests (see / validate / reject)   (if OPS-gated — D5)
3.5    Manage weekly commissions       (calculate / mark paid)
3.6    View all platform transactions
3.7    View platform financial summary
3.8    Manage disputes                 (view all client & agent / resolve)
3.9    Issue a client refund           (new — no prototype path)
3.10   Moderate accounts               (suspend / reactivate / ban)
3.11 * Receive operational alerts      (pending money movements, AML flags)
```

---

## Agent enrollment

Onboarding is **Admin-initiated** (no self-service application in v1 — see D4),
with a **TOS agreement** gate and a **funding** gate. Diagram:
`01-project-specs/02-agent-onboarding.drawio`.

1. **Admin picks a user to promote.**
2. **System:** creates an `enrollment_request` (attaches admin + user), adds the
   interim role **`PENDING_AGENT`** to the account, and sends the user a **TOS
   agreement** notification.
3. **User agrees?**
   - **No** → system removes `PENDING_AGENT`, marks the enrollment `DECLINED`,
     notifies admin. End.
   - **Yes** → system saves the agreement and **creates the agent profile**
     (`status = INACTIVE`, `balance = 0`, `level = N1`), then creates a
     **`ledger_request`** (`type = FUNDING`, `amount = 20 USD`,
     `status = PENDING_AGENT_ACTION`, linked to the enrollment via `enrollment_fk`).
4. **Agent funds:** system sends payment info → agent pays MOKA PAY off-platform
   → uploads proof.
5. **Admin validates the payment.** If correct → agent profile `ACTIVE`,
   `balance = 20`, the `ledger_request` → `DONE`. If not → re-request.
6. **Dispute during enrollment.** Because *admins can also be abusive*, the agent
   can file a dispute (or ask for help/QA) at the funding step instead of paying.

Interim role `PENDING_AGENT` cleanly models "invited but not yet agreed" and is
removed on decline. The agent profile is created **only after** the user agrees.

---

## Balance & money-movement model

The agent's guarantee is the collateral that underwrites every payout. Its money
model has two independent mechanisms — **ledger movements** (change the balance)
and **holds** (reserve funds without changing the balance).

### Agent profile balance fields
```
agent_profile: { status, balance, held, available }
   balance    — settled funds; the "ledger balance". Moved ONLY by ledger_request.
   held       — funds reserved for in-flight transfers / pending withdrawals.
   available  — DERIVED: balance − held. This is what matching checks.
```
`available` is **derived, not a stored/maintained column** (avoid the
denormalized-aggregate desync from `00-init.md`).

*(Naming: `balance / held / available` is standard banking vocabulary — a bank's
"available balance" vs "ledger balance" with holds in between. Optionally rename
`balance → ledger_balance` for clarity.)*

### `ledger_request` — the single way `balance` moves
Every balance change goes through one typed entity. Types:
`FUNDING (+)`, `WITHDRAWAL (−)`, `REFUND (±)`, `DEDUCTION (−)`, `COMMISSION (+)`, …

- **Request vs ledger line (keep separate).** The `ledger_request` is a **mutable
  workflow object** (status moves over time). When it settles it writes **one
  immutable, append-only ledger line** carrying `balance_after`. Do **not** put
  the mutable status and the immutable money-movement on the same row — that
  coupling is exactly where the prototype's double-count / idempotency bug lived
  (`00-init.md`). Settlement must be **idempotent** (`FOR UPDATE` + status guard).
- **Shared status machine:**
  `INITIATED → PENDING_AGENT_ACTION → PENDING_ADMIN_APPROVAL → { DONE | REJECTED | DISPUTED }`.
  Each type declares its direction (sign). WITHDRAWAL runs the same approval gate
  in the opposite direction (admin pays out off-platform, then settles).

### Holds — move `held`, never `balance`
Reserving/locking collateral does **not** change `balance`; it changes `held`
(and therefore `available`). A hold is **not** a `ledger_request`.

Auth → capture pattern (like a card authorization). A hold has two endings:
- **Release** (happy path): `held −= X`. Balance untouched. No ledger_request.
- **Capture** (fault): `held −= X` **and** a `DEDUCTION` ledger_request fires
  (`balance −= X`). This is the **only** crossover between holds and balance —
  and it goes *through* a ledger_request, preserving the invariant.

So: a transfer **reserves** on agent-accept (`held += X`), and on completion
either **releases** (success) or is **captured** (agent at fault). A **withdrawal**
holds while pending, then settles (WITHDRAWAL ledger_request + drop hold).

---

## Transfer lifecycle

Happy path diagram: `01-project-specs/01-transfer-flow-happy-path.drawio`.

### Status machine
```
pending_match
   → pending_agent_approval        (agent matched; agent may accept/decline — see D8)
   → pending_client_transfer       (agent accepted; guarantee RESERVED / held += X)
   → pending_agent_transfer        (client paid off-platform + uploaded funding proof)
   → pending_client_confirmation   (agent paid recipient off-platform + uploaded proof)
   → done                          (client confirmed; guarantee RELEASED / held −= X)

terminal (bad path): disputed → { refunded | done | cancelled } ,  expired
```
Notes carried from the happy-path review: commission is calculated **once** at
creation (it's a flat % of the fee, agent-independent); the exchange rate is
**locked once** at creation (15-min TTL — see D11 for expiry mid-flow); the
guarantee is **reserved on accept and released on done** (a hold, not a debit).

### Dispute / bad-transfer path (design — diagram still TODO)

The only flow that moves `balance`, and where hold/capture, refund, and admin
resolution converge.

**Where a transfer goes bad (by state):**
- `pending_client_transfer` → client never pays → low risk (nobody is out money)
  → timeout → **cancel**, release the hold.
- `pending_agent_transfer` → agent never pays the recipient (but client already
  paid the agent) → **high risk** → dispute.
- `pending_client_confirmation` → client contests "not received" though the agent
  uploaded proof → **contested** → dispute.
- Any → client fraud (fake funding proof, agent paid in good faith) → agent files
  a dispute.

**Core rule — a dispute freezes the hold.** On dispute the transfer goes
`disputed` and the hold **stays held**. Never release until an admin resolves —
the hold is the collateral that funds the fix.

**Admin resolution outcomes:**

| Ruling | Hold | Balance effect | Client outcome | Terminal |
|---|---|---|---|---|
| Agent proven right (recipient was paid) | release | none | — | `done` |
| Agent at fault (didn't pay / vanished) | **capture** | `DEDUCTION` (agent −) | **refund** from captured amount | `refunded` |
| Client fraud (agent paid in good faith) | release | none (make agent whole) | no refund; client sanctioned | `done` |
| No-fault / stuck (client never paid) | release | none | — | `cancelled` |

The **client refund is backed by the captured agent guarantee** — that is the
entire reason the guarantee exists. If capture < refund owed, the shortfall is
covered by platform reserve / agent insurance (`has_insurance`, N4) — see D10.

**Triggers:** client dispute (manual, at confirmation), agent dispute (client
fraud), and **system auto-dispute on timeout** (agent hasn't paid within X). At
`pending_client_confirmation`, if the client neither confirms nor disputes within
Y, the system **auto-confirms** (assume success, release hold) so holds don't get
stuck forever (see D11).

---

## Open decisions

| # | Decision | Proposed for v1 |
|---|---|---|
| **D1** | Multi-role accounts — can one account be both Sender and Agent? | **No** — one role per account. |
| **D2** | `OPS` vs `ADMIN` — two tiers now or collapse? | **Keep the enum values, treat as one actor.** |
| **D3** | Recipient as account — do recipients log in / self-confirm? | **No** — data-only. |
| **D4** | Self-service agent application vs Admin-initiated onboarding. | **Admin-initiated**; application queue later. |
| **D5** | Agent withdrawals — OPS-gated (symmetric with deposits) or direct? | **OPS-gated** (money leaving deserves ≥ scrutiny than money in). |
| **D6** | KYC / monthly limits — capture identity to raise limits? | Deferred; add a gate after `Start transfer` when limits exist. |
| **D7** | Saved beneficiaries — in v1? | Nice-to-have; likely yes (client 1.12). |
| **D8** | Agent decline — model as offer/accept with timeout + re-match, or forced assignment? | **Offer/accept with timeout** (self-healing queue). |
| **D9** | Commission — flat 60% for all agents, or higher share for higher levels? | **Flat for v1**; level affects capacity/priority only. |
| **D10** | Refund shortfall — if captured guarantee < refund owed, who covers it? | Platform reserve / agent insurance; escalate to ops. Name it, defer. |
| **D11** | Rate-lock expiry mid-flow & client-confirmation timeout. | Auto-re-quote on lock expiry; **auto-confirm** after client timeout. |

---

## Next

- **Data entities (fields):** Account, Agent profile (with `balance/held/available`),
  `enrollment_request`, `ledger_request` (+ immutable ledger line), Transfer, Proof,
  Commission — the nouns and their columns, done our way (native `ENUM`s, one source
  of truth per fact, views-for-reads / procedures-for-writes, per-service schema
  isolation). See `.claude/rules/sql.md`.
- **Dispute / bad-transfer diagram** (root `TODO.md`).
- Resolve the open decisions above.
