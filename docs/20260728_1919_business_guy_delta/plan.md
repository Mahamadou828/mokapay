status: approved

# Business-guy prototype delta — what's actually missing

Source of the comparison: `tmp/mokapay` (the non-developer's prototype — reference
for product intent only, never ported as-is) against the current state of this
repo (`app/services/{gateway,user,payment,file}`, `app/ui/{mokapay-mobile,mokapay-admin}`),
cross-checked against the design docs already on file: `docs/01-init/00-init.md`
(prototype teardown), `docs/01-init/01-project-spec.md` (our actual spec, incl.
Open Decisions D1–D11), and `docs/20260724_1631_payment-service/plan.md` (approved,
mid-flight).

**Headline finding: this is much smaller than it looks.** The prototype has 9
services + 3 frontends; we have 4 services + 2 frontend submodules already in
progress (`mokapay-mobile`: client+agent PWA, quite far along; `mokapay-admin`:
accounts/enrollments/ledger). Most of the prototype's product intent is already
either (a) implemented here, just organized differently, or (b) already designed
in an approved doc and simply not built yet, or (c) already deliberately deferred
on purpose. Only one capability class (compliance/AML) has no design at all yet.

---

## 1. Already covered — different shape, same result (no action)

Per your instruction, these don't need porting — just noting so nothing gets
rebuilt by accident:

- **Auth**: prototype = custom JWT + bcrypt OTP. Ours = real AWS Cognito +
  OTP 2FA. Ours is strictly more robust (managed token lifecycle, no
  hand-rolled JWT signing).
- **Remittance state machine**: prototype's `transaction-service`
  (PENDING→ASSIGNED→IN_PROGRESS→PROOF_SUBMITTED→COMPLETED, sender-confirm,
  60-min expiry) is functionally our `transfer` domain's status machine
  (`pending_match→pending_agent_approval→pending_client_transfer→
  pending_agent_transfer→pending_client_confirmation→done`), proof-gated
  transitions, and River-scheduled auto-confirm/timeout jobs. Ours is the
  more complete implementation (guarded transitions, idempotent proof
  redelivery, real timers) — the prototype's version has no transition
  guards and never enforces its own expiry (teardown finding #6).
- **Agent matching**: prototype's 7-weighted-criteria scoring is real logic;
  ours (`payment/internal/business/matching`) does the same job (broadcast
  offers, weighted rank, timeout + re-match, first-accept-wins) and is
  already live, not a stub.
- **Guarantee deposit / agent funding**: prototype's bespoke `deposit_requests`
  table+workflow is our generic `ledger_request` (type `funding`,
  `pending_agent_action→pending_admin_approval→done/rejected`) — same OPS-
  validated top-up flow, one mechanism instead of a parallel table.
- **Proof upload + tamper evidence**: prototype's SHA-256 hash + HMAC
  signature on `documents` ≈ our `file` service (SHA-256 hash already
  computed on `ConfirmUpload`). Proof-crossing (both sides uploaded ⇒ unlock
  next state) is handled by our transfer status machine directly rather than
  a side "cross-proofs" step — same effect.
- **Fees**: prototype's 1.5% same-currency / 2.0% cross-currency fee +
  0.5% FX margin is already the exact model in
  `payment/business/transfer/usecase` (`FeeSameBps`/`FeeFXBps`) — confirmed
  matching, not a gap.
- **Frontends**: prototype's `apps/web`+`apps/agent` ≈ `mokapay-mobile`
  (client + agent flows already scaffolded: send, transfer tracking,
  beneficiaries, history, agent dashboard/offers/wallets/withdraw/funding/
  commissions). Prototype's `apps/backoffice` ≈ `mokapay-admin` (accounts,
  enrollments, ledger already built). No "build 3 frontends" work needed —
  only new pages for whatever new backend capability lands (see §2, §3).

---

## 2. Gap A — commissions & disputes (payment service, Slice 6)

**This is not a new design — it's the next unstarted slice of an already-approved
plan.** `docs/20260724_1631_payment-service/plan.md` §3 ("Commission") and the
Slices & Progress list already spec this in full:

> On `transfer → done`: accrue one `commissions` row (transfer_fk UNIQUE — single
> bookkeeping place, unlike the prototype's triple-bookkeeping bug): agent 60% of
> fee, platform 30%, guarantee fund 10%, FX margin 100% platform, `accounting_week`
> (Tue→Mon). Weekly Tuesday batch (`commission_batches`) groups unpaid accruals
> per agent and settles each through a `commission` ledger_request — a River
> periodic job, with a `settle-commissions` CLI as the manual/ops re-run entry
> point.

Confirmed in code: `ledger.TypeCommission` / `LEDGER_REQUEST_TYPE_COMMISSION`
exist as an enum value end-to-end (proto → mapper → sqlc) but **nothing ever
creates one** — no usecase path fires it on transfer completion. The prototype's
`RecordCommission`-on-confirm + weekly-Tuesday-payout is exactly what's designed
and unbuilt here. The prototype's actual bugs (non-idempotent ledger, agent-id
vs user-id confusion, hand-rolled `rattrapage.go`/`rattrapage_commissions.py`
backfill scripts with hardcoded single-agent misattribution and unparameterized
SQL via shell) are precisely what the `transfer_fk UNIQUE` constraint and
native-enum/single-source-of-truth conventions already prevent — **do not port
the rattrapage scripts or their approach in any form.**

Also in this slice per the plan: **disputes** — `disputes` table, admin
resolution enum (`agent_right→release,done` / `agent_fault→capture+refund,
refunded` / `client_fraud→release,done` / `no_fault→release,cancelled`),
hold-stays-held-while-disputed, system auto-dispute on agent-payout timeout,
auto-confirm on client-confirmation timeout (D11), refund-shortfall logged to
ops (D10, `platform_reserve` named but deferred).

**Scope for this slice** (from the plan, §5/§6/§8):
- Schema: `commissions`, `commission_batches`, `disputes` tables (`payments`
  DB) + views/procedures per SQL rules.
- Business domains: `payment/internal/business/commission`,
  `payment/internal/business/dispute`.
- River: Tuesday periodic job (batch settlement) + timeout/auto-dispute/
  auto-confirm jobs (extends the existing `transfer/events` job set).
- CLI: `settle-commissions` manual re-run command (mirrors existing
  `cmd/migrate,seed,reset` pattern).
- Proto additions: `TransferService.OpenDispute`, `AgentService.ListCommissions`,
  `AdminPaymentService.ResolveDispute` (per plan §6).
- Gateway routes for the above (client: open dispute; agent: list
  commissions; ops: resolve dispute, view commission batches).
- **New admin-app pages**: a commissions/finances view (batch status, mark-paid
  history — the equivalent of the prototype's `Finances.jsx` idea, not its
  code) and a disputes queue (list + resolve action) in `mokapay-admin`.
- **Mobile-app**: agent's existing `pages/agent/commissions/Commissions.tsx`
  page already exists as a stub in the submodule — wire it to the new
  `ListCommissions` RPC once it exists; add a client-side "open dispute"
  entry point on the transfer detail/tracking screens.

This is the one item I'd propose starting immediately — it's fully designed,
has no open architecture question, and unblocks agent payouts (currently a
confirmed no-op).

---

## 3. Gap B — compliance / AML (no existing design — needs your input)

Unlike §2, this has **no design doc anywhere**. `01-project-spec.md` only
mentions it once, as an admin capability: *"3.11 Receive operational alerts
(pending money movements, AML flags)"* — no rule engine, no risk model, no
service boundary decided.

The prototype's `compliance-service` is a real, mostly-legitimate AML rule
engine worth mining for the rule *logic* (not its code): per-transaction
evaluation against a fixed rule set, each contributing to a weighted 0–100 risk
score (LOW/MEDIUM/HIGH/CRITICAL at 30/60/80), feeding an alert lifecycle
(open→under review→escalated→resolved/dismissed) and case management
(bundling alerts, closing with/without a Suspicious Activity Report).

Rules worth keeping (product logic, values need your sign-off, not mine):
daily volume limit, weekly structuring/smurfing (fractionation under the daily
threshold), user volume spike (≥5× 30-day average), agent volume spike (≥3×),
beneficiary concentration (≥10 distinct senders to one recipient in 7 days),
hourly velocity (≥5 tx/hour), night-time structuring pattern, new-account
high-volume (&lt;1 day old, &gt;$500).

**Explicitly not porting**: the prototype's "PEP screening" is a hardcoded
3-prefix phone blocklist — not real screening, and actively worse than doing
nothing (gives false confidence). Treat PEP/sanctions screening the same way
this repo already treats SMS delivery: a named, deferred integration point
("needs a real provider — ComplyAdvantage/WorldCheck or similar — picked
later"), not something to fake.

This also reopens **D6 (KYC / monthly limits)**, currently "deferred" in the
spec's Open Decisions — several of the AML rules above (daily/weekly limits,
volume-spike-vs-average) implicitly assume some notion of a running volume
figure and a limit to compare it against, which doesn't exist yet (`account`
has no `kyc_level` or `monthly_limit` today). Building compliance without also
resolving D6 means the limit-based rules have nothing to check against.

**Needs a decision before I implement anything here** (see questions below):
own microservice (`compliance`, matches the workspace's one-service-per-domain
pattern, evaluates async via a River job the way `transfer` events already
work) vs. a business domain inside `payment` (simpler, but couples a
cross-cutting concern to one service and every other service that wants
AML coverage later would need to reach into payment for it).

---

## 4. Confirmed deliberately deferred — no action without an explicit ask

Both of these are already *decided*, not just unimplemented — I'm flagging
them so they don't get silently "fixed" as part of this pass:

- **Reporting service**: `01-init/01-project-specs/04-infrastructure.md`
  marks it explicitly `Reporting *(later)*` — and the prototype's own
  `reporting-service` is an empty shell (no code at all, just a Dockerfile),
  so there's nothing to mine from it either. Recommend: leave deferred.
- **KYC / monthly transfer limits (D6)**: spec says "Deferred; add a gate
  after `Start transfer` when limits exist." Only worth resurfacing bundled
  with §3 (compliance) above, since compliance's limit-based rules need it —
  not as a standalone feature otherwise.

---

## 5. Small, concrete, non-architectural gap

- **Real SMS provider for OTP delivery.** `user/internal/integration/sms`
  is a log-only stub by design ("provider selection deferred... DRC routes
  need real-world testing" — already a known, intentional TODO, not
  something I "discovered"). This blocks real phone delivery in any
  non-dev environment. The prototype used Africa's Talking in production,
  which is a reasonable, DRC-market-appropriate default if you don't already
  have a preferred vendor. Small, isolated change (`sms.Sender` interface
  already exists — one new implementation, no usecase changes).

---

## 6. Explicitly not porting (insecure / nonsensical in the prototype)

- `rattrapage.go` / `rattrapage_commissions.py` — hardcoded single-agent-for-
  every-transaction commission misattribution, plaintext DB password in
  source, unparameterized SQL string-interpolated through a shelled-out
  `docker exec psql`. Our commission design (§2) is idempotent by
  construction (`transfer_fk UNIQUE`), which is exactly what removes the
  need for this class of script ever existing.
- Hardcoded PEP phone-prefix blocklist (see §3) — not real screening, do not
  carry the mechanism over even as a "temporary" measure.
- Simulated multi-source FX aggregation (`math/rand` jitter around hardcoded
  base rates dressed up as an 8-source weighted composite) — our real
  HTTP-provider-with-fallback (`payment/internal/integration/fxrate`) is
  already the more honest implementation. Only open item: it's USD:CDF only
  today vs. the prototype's intended (if fake) multi-corridor design —
  whether we need more real corridors is a product question, not a
  security/quality one (see questions below).
- Shared identical dev secrets across every service's `.env`, single shared
  Postgres database across all 9 services, `VARCHAR`+CHECK pseudo-enums,
  `agent_id` meaning two different foreign keys depending on table,
  commission bookkeeping in three different places — all already correctly
  avoided by this repo's existing per-service-DB / native-enum / single-
  source-of-truth conventions. Nothing to action, just confirming the
  prototype's mistakes aren't being repeated.

---

## Decisions (2026-07-28)

Confirmed: build §2 (commissions/disputes) and §3 (compliance/AML). Skip §5
(SMS provider) — no vendor picked yet, revisit later. §4 (reporting, KYC)
stays deferred as originally recommended.

- **Compliance shape**: own microservice (`compliance`), evaluated async via
  a River job from `payment` — never blocks a transfer. Full design:
  [compliance-service.md](compliance-service.md).
- **KYC tiers (D6)**: turns out **not needed** for this compliance design —
  the rule set uses fixed global thresholds (e.g. a flat $10k/day config
  value) and relative-to-own-history multipliers (5×/3× a 30-day average),
  not a per-account KYC-tier limit. D6 stays deferred, untouched by this work.
- **FX corridors**: out of scope for this pass (not requested) — USD:CDF only,
  unchanged.
- **PEP/sanctions screening**: explicitly not built (see compliance-service.md)
  — deferred pending a real provider decision, same treatment as SMS.

## Implementation slices

1. **Payment: commissions + disputes** (§2) — self-contained within
   `payment`, no dependency on compliance. Build first/independently.
2. **Compliance service** (§3, full design in compliance-service.md) —
   brand-new service, no file overlap with (1). Build independently.
3. **Payment→compliance integration** — payment computes the rolling metrics
   (`transfers`/`ledger_lines` queries) and enqueues the `EvaluateTransfer`
   River job. Sequenced *after* (2) exists (needs the compliance proto
   client) — small, final wiring step.
4. **Admin app pages** — commissions/disputes view + compliance alerts/cases
   view in `mokapay-admin`, once (1) and (2) have working RPCs to call.

---

## Progress

- [x] Slice 1 — payment commissions + disputes (2026-07-28). Backend only:
      schema (`commissions`, `commission_batches`, `disputes` + views and
      procedures), the `commission`/`dispute` business domains, accrual on
      every path to `done`, the Tuesday River job + `settle-commissions`
      CLI, the resolution table with its money effects, payout-timeout
      auto-dispute, protos and gateway routes. No reconciliation script —
      accrual is idempotent by `transfer_fk UNIQUE` and the weekly payout
      by `(agent_profile_fk, accounting_week) UNIQUE`. The admin-app and
      mobile-app pages named in §2 remain Slice 4.
- [x] Slice 2 — compliance service (2026-07-28). New `compliance` service
      (own `compliance` DB, gRPC :50054): schema (`rule_evaluations`
      append-only, `alerts`, `cases`, `case_alerts`, 5 native enums),
      `compliance_ext` views (incl. the computed `account_risk` and
      `dashboard_counts`) and procedures, the 8 rules in
      `business/evaluation` reading every threshold from env config, case
      management in `business/investigation`, `proto/compliance/v1`
      (internal `ComplianceService.EvaluateTransfer` + `AdminService`),
      gateway ops routes under `/v1/admin/compliance/*`, compose wiring.
      No PEP/sanctions screening (deferred, see compliance-service.md). No
      caller yet — payment-side metrics + River job are Slice 3.
- [x] Slice 3 — payment → compliance integration (2026-07-28). The
      `payment/internal/business/compliance` domain: three read-only
      aggregations over `payments_ext.transfers` (`db/query/metrics.sql`,
      sender / agent / recipient) plus the recipient-phone index
      (`0007_transfer_metrics_index.sql`), the snapshot assembly, and the
      `compliance.evaluate_transfer` River job enqueued transactionally
      with the completion that earns it. Both `→ done` paths route through
      one `onCompleted` hook (commission accrual + evaluation), and the
      dispute resolutions that also land on `done` fire the same pair.
      Amounts convert minor units → whole USD once, in the business layer,
      matching the contract. Account age (the one input payment does not
      own) comes from `user`'s `AdminService.GetAccount` under a system
      passport, uncached — the job is already off the critical path. A
      failed evaluation is logged and dropped, never propagated: an
      unresolvable account is permanent (no retry), anything else retries
      to the repo's standard 3-attempt bound. Config: `COMPLIANCE_GRPC_PORT`
      and `COMPLIANCE_TIMEZONE` (`Africa/Kinshasa` — the night-time window
      is a local-clock question). Verified end-to-end against live
      `user` + `compliance`: 8 rules evaluated, 4 triggered, rows in
      `rule_evaluations`.
- [x] Slice 4 — frontend pages (2026-07-28). **mokapay-admin**: three new
      pages following the `pages/ledger` layout — `pages/commissions`
      (weekly batch list; the row links to the ledger request that actually
      pays it rather than duplicating an approve button), `pages/disputes`
      (queue + the 4-way resolution panel, each option spelling out the
      money it moves), and `pages/compliance` (dashboard counts + alerts
      worklist with status moves and case linking + cases tab). New
      `api/{commission,dispute,compliance}`, the enum chips/labels for all
      of it in `components/ui/Chip.tsx`, routes and sidebar entries.
      **mokapay-mobile**: `pages/agent/commissions` now reads the real
      `GET /agent/commissions` accruals (the old code read COMMISSION
      *ledger requests*, which only exist once the Tuesday batch runs — a
      full week of earnings showed as zero); new `data/commission.ts`
      totals over `payable_amount`, the only cross-row-summable figure. New
      `ui/DisputeAction.tsx` on the client transfer Tracking + Detail
      screens, shown only on the three post-accept states the engine
      accepts a dispute for. `TRANSFER_STATUS_REFUNDED` added to both apps'
      status unions (it was missing and is reachable via `agent_fault`).
      All TS types verified field-by-field against the protos rather than
      guessed — the gateway marshals with `UseProtoNames: true`, so the
      wire is snake_case. Verified: `tsc -b`, `oxlint` and the production
      `vite build` clean for both apps, run on `node:22-alpine` (the repo's
      own UI build image) because the host Node 18 cannot run Vite 8.
      Not verified: no browser automation available, so the pages were
      never clicked through against live data — only the dev server boot
      and module graph were smoke-tested.
