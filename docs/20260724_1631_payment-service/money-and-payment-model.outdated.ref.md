# 04 — Money & Payment-Method Model

> Source of truth for currencies, payment methods, and how a transfer's currency
> pair is handled. Supersedes any earlier mention of **EUR** — that was a mistake
> and must be purged everywhere (spec, handoff, mock).

---

## Currencies

Exactly **two**: **USD** and **CDF** (Congolese Franc). **No EUR.**

Every money value in the system is `{ amount, currency }` — a bare number whose
currency is inferred from a variable name (`amountEur`, `*_usd`) is banned. That
naming-by-convention is the exact bug the prototype shipped (`00-init.md`:
`*_usd` columns holding EUR).

---

## Payment methods (operators)

For **transfers**, three mobile-money operators:

- **Orange Money**
- **M-Pesa**
- **Airtel Money**

Additional channels exist **only for agent guarantee deposits** (out of scope for
transfers): Afrimoney (mobile money), Rawbank and Equity BCDC (banks).

### Currency is a property of the wallet, not the operator

The DRC is heavily dollarized: the **same operator can hold a USD wallet or a CDF
wallet**. So a "USD → CDF" transfer can be a **USD** Orange Money wallet → a
**CDF** Orange Money wallet. You therefore **cannot infer currency from the
operator** — currency is stored explicitly on the wallet:

```
Wallet = { operator, currency, phone/account, status }
         currency ∈ { USD, CDF }
```

---

## Corridors

A transfer goes from a **source** wallet to a **destination** wallet. Exchange is
applied **iff the two currencies differ**.

| Source | Destination | Exchange? | Fee tier |
|---|---|---|---|
| **USD → CDF** | different | ✅ FX rate applied | ~2.0% (cross-currency) |
| **CDF → CDF** | same | ❌ no FX | ~1.5% (same-currency) |
| **USD → USD** | same | ❌ no FX | ~1.5% (same-currency) |

The **rule**, not the table, is what to implement:

```
hasExchange = (sourceCurrency != destinationCurrency)
feePct      = hasExchange ? 2.0% : 1.5%
```

- **Same-currency** (`CDF→CDF`, `USD→USD`): no rate, no FX margin, lower fee.
- **Cross-currency** (`USD→CDF`): apply the FX rate **plus** the 0.5% FX margin,
  higher fee. Indicative rate `1 USD ≈ 2 830 CDF`.

Both wallets (source and destination) must be on an operator that **supports that
currency** — that's what "if the operator supports it" means for `USD→USD`.

---

## Transfer shape

```
Transfer = {
  sourceCurrency:      USD | CDF,
  destinationCurrency: USD | CDF,
  operator:            Orange Money | M-Pesa | Airtel Money,   // recipient payout channel
  amountSource, feeAmount, feePct,
  rate, marginAmount,        // present only when hasExchange
  amountDestination,
  hasExchange,               // = sourceCurrency != destinationCurrency
  ...
}
```

---

## Agent guarantee & internal accounting: USD

The agent's guarantee balance, `ledger_request`s, and commissions are all
denominated in **USD** — the single internal accounting currency. (The mock's
`depositEur` / `...Eur` fields were part of the EUR mistake and get renamed to
USD.)

- Guarantee minimum to activate: **20 USD**.
- `ledger_request.amount` carries an explicit currency (USD).
- Commission = 60% of the fee, accrued and paid in USD.

---

## Impact on matching

Wallet-fit in the [matching algorithm](./03-matching.md) must check **operator
AND currency**, not operator alone. An agent can only fulfil a transfer if they
hold a wallet matching the destination's `{ operator, currency }`. The amount /
guarantee comparison must also be currency-consistent (compare like-for-like,
converting to the USD accounting currency where needed).

---

## Open points

- **CDF → USD** — not in the confirmed set (we have `USD→CDF`, `CDF→CDF`,
  `USD→USD`). Is a CDF-source → USD-destination transfer allowed? Assumed **no**
  for now; revisit if there's demand.
- **Sender → agent rails.** How a diaspora sender actually pays (direct to the
  agent vs into a MOKA PAY collection account, and via what method) is still
  unspecified and defines the sender-side payment experience. Needs a decision.
- **FX source & lock.** Where the `USD:CDF` rate comes from and how long it's
  locked per transfer (prototype: Redis, 15-min lock) — see `00-init.md`.

---

## Cleanup owed

Every existing artifact still referencing **EUR** must be corrected to this model:
- `design-docs/01-project-spec.md`, `02-design-handoff-mobile.md`, `03-matching.md`
- `apps/web/src/data/*` (mock: `RATE_EUR_CDF`, `amountEur`, `depositEur`, `commissionEur`, …)
- the design-reference brief
