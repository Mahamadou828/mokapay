# 03 — Agent Matching

> How a transfer is matched to an agent. The scoring model is inherited from the
> prototype (`tmp/prototype/`, see [`00-init.md`](./00-init.md)); the **offer /
> accept** wrapper is our change (open decision D8 in
> [`01-project-spec.md`](./01-project-spec.md)). This doc is the source of truth
> for how matching should behave when rebuilt.

---

## Overview

Matching is a **two-stage funnel**:

1. **Pre-filter (SQL)** — a cheap query applies the hard eligibility gates and
   returns a small shortlist of the best-ranked candidates.
2. **Score (in memory)** — each shortlisted agent is scored 0–100 across seven
   weighted criteria; the list is ranked by score.

The prototype then forced the single top agent onto the transfer. **We change the
tail:** matching produces a **ranked list**, and the transfer is *offered* down
that list — each agent can accept or decline, with a timeout that moves to the
next candidate (see [§Offer flow](#offer-flow)).

---

## Stage 1 — Pre-filter (hard gates)

One query selects only agents that are eligible at all, ordered so the strongest
candidates come first, capped at 20:

```sql
SELECT ...
FROM agents a
LEFT JOIN users u ON u.id = a.user_id
WHERE a.status = 'ACTIVE'                                   -- must be active
  AND a.max_transaction_usd >= :amount                     -- guarantee ceiling covers the amount
  AND (:walletIn = '' OR :walletIn = ANY(a.wallets))        -- holds the inbound operator
  AND (:city    = '' OR a.city = :city)                     -- same city (if provided)
ORDER BY a.level DESC, a.success_rate DESC, a.active_transactions ASC
LIMIT 20;
```

An agent that fails any gate never reaches scoring. The `ORDER BY … LIMIT 20`
pre-sorts by **level → success rate → least loaded**, which also becomes the
tie-break order in stage 2.

**Known prototype shortcuts to fix on rebuild:**
- `city` is hardcoded to `"Kinshasa"` by the gateway — should come from the transfer.
- `amount` is labelled USD but isn't normalised to USD (the currency trap from
  `00-init.md`). Match on an explicit `{amount, currency}`.

---

## Stage 2 — Weighted score (0–100)

Each candidate is scored across **seven weighted criteria**. Two are
**eliminatory**: failing them forces the *entire* score to 0 (agent discarded),
not merely a zero slice.

| # | Criterion | Max | Scoring |
|---|---|---:|---|
| 1 | **Availability** | 25 | **Eliminatory** if not `ACTIVE`. Else by active load: `0`→25, `≤2`→20, `≤4`→15, else 10 |
| 2 | **Guarantee** | 20 | **Eliminatory** if `max_transaction_usd < amount`. Else by ratio `guarantee_balance / (amount+1)`: `≥5`→20, `≥3`→18, `≥1.5`→15, else 10 |
| 3 | **Wallet fit** | 20 | holds **both** in+out operators→20, **one**→10, **neither**→0 |
| 4 | **Success rate** | 15 | new agent (`0` transactions)→**8 (neutral)**; `≥99%`→15, `≥97%`→12, `≥95%`→10, else 5 |
| 5 | **Level** | 10 | N1→4, N2→6, N3→8, N4→10 |
| 6 | **Geo proximity** | 5 | city matches (or none given)→5, else 0 |
| 7 | **Current load** | 5 | `0` tx→5, `≤2`→4, `≤4`→2, else 0 |
| | **Total** | **100** | |

**Selection:** drop any agent with `score == 0`, sort by score descending, tie-break
by the stage-1 order. Empty result → no match (transfer stays in the queue).

---

## Design rationale

- **Two hard gates (active + sufficient guarantee).** You can never be offered a
  transfer you're not cleared to cover. This is the money-safety floor.
- **Load-balancing is weighted twice** — availability (25) and current load (5) both
  reward idle agents, so ~30% of the score actively spreads work rather than piling
  it on one person.
- **Guarantee depth is rewarded, not just met** — clearing the ceiling gets you in;
  holding ~5× the amount earns full marks. More collateral → more trusted with
  bigger jobs.
- **New agents aren't punished** — a 0-transaction agent gets a *neutral* 8/15 on
  success rate (not 0), so they can win early jobs and build a record. Deliberate
  cold-start handling.
- **Level and geo are tie-breakers, not drivers** — 15 of 100 points combined.
  Seniority nudges; it doesn't dominate.

---

## Offer flow (our change vs. the prototype)

The prototype matched to a **single** agent and force-assigned the transfer — no
accept/decline. We replace that with an **offer walk down the ranked list**:

1. Matching returns the **ranked candidate list** (not one winner).
2. The transfer is **offered** to the top candidate → status
   `pending_agent_approval`. A **hold is reserved** on that agent's guarantee for
   the offer.
3. The agent **accepts** → transfer proceeds (`pending_client_transfer`), hold stays.
4. The agent **declines** or the **offer times out** → release the hold, add the
   agent to an **exclusion set**, and offer to the **next** candidate.
5. List exhausted / no candidates → transfer returns to `pending_match` to be
   re-matched later (the queue loop — intentionally allowed to retry; see D8).

Consequences for the implementation:
- The scorer must return a **sorted list**, and re-matching must **exclude** agents
  who already declined/timed out this round.
- Each offer places a **guarantee hold** (`held += amount`) that is **released** on
  decline/timeout — never a balance movement (see the hold/capture model in
  `01-project-spec.md`).
- Excessive declines should feed back into the agent's stats / score (abuse guard),
  so cherry-picking only high-commission offers is penalised over time.

---

## Open points

- **Offer timeout duration** — how long an agent has to accept before re-queue.
- **Decline penalty** — whether/how decline rate lowers the matching score.
- **Multi-city / geo** — the prototype assumes one city; a real deployment needs
  the transfer to carry the target city and the query to use it.
- **Currency-aware matching** — wallet fit should check operator **and** currency,
  and the amount gate should compare like-for-like currencies (see the wallet /
  money model note).
