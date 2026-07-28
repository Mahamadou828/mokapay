status: done

# Frontend integration — mobile PWA (client + agent) & admin app

Two frontends against the gateway REST surface:

- **`mokapay-mobile`** (existing repo, submodule at `app/ui/mokapay-mobile`)
  — ONE installable PWA serving both roles from the "Registre" design
  (`design/design_handoff_moka_pay_mobile/`, 36 hifi frames, FR-only).
  C1 is the welcome screen (login / create account); after login the
  account's role decides the experience (client tabs vs agent tabs). A
  `pending_agent` sees the client app + the enrollment flow. C1 asked
  "utilisateur ou agent ?" up to slice 12 — see the entry there for why
  that question had no answer worth asking.
- **`mokapay-admin`** (new repo, submodule at `app/ui/mokapay-admin`) —
  desktop React app for ops/admin: accounts, enrollments worklist, ledger
  approvals; transfers/disputes when they exist. No design bundle — plain
  pragmatic tables/forms; not a PWA.

Stack per the workspace frontend rules: Vite + React + TS + Tailwind v4 +
React Router + Redux (`store/<domain>`), pages under
`pages/<domain>/<page>/` with local `ui/`. The mobile repo's scaffold
(React 19, Tailwind 4, oxlint) stays.

Ownership proposals: **[C]** implementation, **[M]** design deltas &
review.

## 1. Design ↔ backend deltas (the "some things need to change" list)

| # | Design says | Backend reality | Resolution |
|---|---|---|---|
| D1 | **Passwordless** (C2 phone → C3 OTP, "no password" note) | Cognito: register/login = phone + **password** + OTP | **v1 follows the backend**: C4 gains a password field; login = C2 phone+password → C3 OTP. Passwordless = Cognito custom-auth Lambda flow — a backend project, revisit post-v1 |
| D2 | C4 has no email | `Register.email` is optional | No screen change — omit; add later in Profil |
| D3 | G1 terms checkbox | `AcceptEnrollment` requires `tos_version` matching the backend's | G1 displays the version returned by `GET /v1/enrollment`… actually fetched with the TOS copy; the accept payload echoes it |
| D4 | C6 shows live rate, C7 simulator | ~~No fx service yet~~ **resolved**: fx lives behind `POST /v1/transfers/quote` (stateless; the rate locks at create) | Both built in slice 7. C6's rate row is a 1 USD quote; C7 debounces the quote per keystroke |
| D5 | Status chips map many transfer states | Transfers don't exist yet | Chip component built now (notifications/enrollment/ledger use it); transfer statuses wired in phase 2 |
| D6 | "Se déconnecter" only | Backend also has change-password (reset flow) | Add to C19 Sécurité group (uses the RESET OTP flow) |
| D7 | Amounts in **EUR**, "1 € = 2 850 CDF" | `Currency` is USD/CDF only; corridors usd→cdf, usd→usd, cdf→cdf | v1 sends **USD**. Also: minor units are **per-currency** — USD = cents, **CDF = whole francs** (`cents × rate / 1e8`). `formatMinor` is scale-aware; a flat ÷100 showed CDF 100× too small |
| D8 | C11/C15 show the agent's name, ★ rating, level, transfer count | `Transfer` exposes only `agent_payment_details` (operator + msisdn) to the client — by design | Identity card dropped; C11 keeps operator/number/reference (copy-to-clipboard), C15 drops the "Agent" row |
| D9 | G16 rating, success rate, transfer count | `AgentProfile` has status, level, balance/held/available, guarantee_floor — no reputation domain | G16 = level ladder + guarantee + profile status. Reputation is a backend project |
| D10 | G7 "Votre commission", G8 per-transfer "+1,50 €", G14 weekly Tuesday payout | `LedgerRequestType.COMMISSION` exists but **nothing computes the agent's share** ("arrives with its volley"); no payout schedule | No commission figure is invented. G7 shows "Le client vous verse" + the hold; G8 drops the fee line; G14 lists real COMMISSION ledger requests (empty until the engine writes them) and says commissions credit the balance on approval |
| D11 | G15 "Par défaut" wallet chip | No default: one wallet per {operator, currency}, picked by the transfer's corridor | Card shows the currency instead; "remove then re-add" note for edits |
| D12 | C8 has no payment-source field | `CreateTransfer` requires `from_operator` | C8 gains a "Vous payez depuis" operator group |
| D13 | C14 "Signaler un problème", C15 "Ouvrir un litige" | `DISPUTED` is reserved and unreachable in v1 | Omitted — disputes (C20/G18) are out of scope per the handoff |
| D14 | C10 implies a live match push | No server push for transfer moves | C10–C14 poll (4 s while matching, 15–20 s while waiting on the other party); the elapsed timer counts from `created_at` so a reload can't reset it |
| D15 | G2/G12 list six deposit channels with account references | ~~No catalogue anywhere~~ **resolved**: `GET /v1/agent/deposit-channels`, served from the payment service's `DEPOSIT_CHANNELS` config and validated at boot | The app renders whatever the list returns and says so when it is empty — no invented MSISDNs. Ships with M-Pesa / MOKA PAY SARL only; ops open the rest by editing the env var |
| D16 | Deposit reference `DEP-2026-01234` | ~~No such field~~ **resolved**: `LedgerRequest.ref`, server-generated and unique, prefixed by type (DEP/WDR/RFD/DED/COM) | Rendered verbatim; the planned client-derived `DEP-<uuid prefix>` is dropped |
| D17 | G3 "canal utilisé" + "numéro émetteur", G13 "verser sur" | `CreateLedgerRequest` carries type, amount, an optional typed reference and free-text `reason` — no fields for any of these | They ride in `reason` ("Dépôt M-Pesa · +243…", "Retrait Orange Money · +243…"), which is what ops read next to the request. Prose, not structured data |
| D18 | — | An agent's own FUNDING was created at `pending_admin_approval`, a state the proof worker cannot transition into | **Resolved backend-side**: agent requests now land at `pending_agent_action`, so G12's proof attaches exactly like G2/G3's. Verified live |
| D19 | G11 rows show type + status chip, incl. pending ones | `LedgerLine` has only signed amount, `balance_after`, `ledger_request_id`, `created_at` — no type, no status, and it exists only after settlement | The feed is the **request** list; lines join in by `ledger_request_id` to annotate settled rows with the resulting balance |
| D20 | G13 "Minimum : 10 USD" | No minimum exists. The real rule is the ceiling: `add_ledger_request` rejects a withdrawal breaking `available − guarantee_floor` | Minimum dropped; the screen shows *Disponible* and "dont X non retirable (garantie)", derived the same way. Verified: 600 rejected, 300 accepted at available 2500 / floor 2000 |
| D21 | G13 pays out to a chosen wallet | The request cannot reference a wallet (D17) | The wallet rides in `reason`; with no wallet declared the screen routes to G15 instead of submitting something ops cannot pay |
| D22 | G2 is reachable straight after accepting | `Accept` creates the agent profile; the guarantee funding is opened by **ops**, separately | G4 shows whichever wait is real — "un administrateur doit ouvrir votre demande" until the request exists, then the *Financer la garantie* CTA |
| D23 | G3 asks for the channel and sender number on the proof screen | By then the request exists and only its *creation* carried `reason` | A self-served top-up captures them on G12 (before create); the ops-created guarantee relies on the capture itself, which shows both |
| D24 | — | `AgentProfile.currency` is a bare ISO code (`"USD"`), not the `CURRENCY_*` enum every other payment message uses | `profileCurrency()` converts at the edge. Before this, every agent-side amount formatted as `NaN` — a slice-7 bug that only shows with a real agent profile |
| D25 | C7/C8 show one amount field | ~~The app hardcoded usd→cdf~~; the engine serves **three** corridors (usd→cdf, usd→usd, cdf→cdf — only cdf→usd is refused) and now quotes **both directions** (`GetQuoteRequest.to_amount`) | The two-card converter: a currency pill per card and a swap control that moves which side you type into. The untyped card always shows the server's figure, never one computed client-side. With CDF on the left the right pill is locked — cdf→usd is not a corridor |
| D26 | G15 "one wallet per {operator, currency}" | **Changed backend-side**: one wallet per **operator**, with `accepts_usd`/`accepts_cdf` flags (at least one). `currency` is a reserved field | The currency picker became two toggles; the duplicate check is on operator alone. The app was sending the retired `currency` and got `internal error` — the screen was broken against the live contract until this |

## 2. Screen → API map (what's buildable today)

**Ready now** (gateway routes live):
- C1–C4 + login: `POST /v1/auth/otp|register|login|refresh|password`
- C18/G17 notifications: `GET /v1/notifications`, `POST …/{id}/read`,
  `…/read-all`; push: `GET /v1/push/vapid-key`, `POST/DELETE
  /v1/push/subscriptions`
- C19 profil: `GET/PATCH /v1/me`, logout
- G1 invitation & terms: `GET /v1/enrollment`, `POST
  /v1/enrollment/accept|decline`
- Admin: `/v1/accounts*`, `/v1/enrollments*`, promote/approve/cancel

**Blocked on gateway routes (backend logic exists)** — see §5:
- G2–G4 guarantee funding, G11–G13 balance/movements/top-up/withdraw →
  LedgerService (engine [M] in flight) + file proofs
- Proof uploads everywhere (G3, G10, C12…) → file-service gateway routes
  (file doc slice 5, still open **[C]**)

**Blocked on backend domains that don't exist yet**:
- C6 (rate) & C7 → fx; C8–C16, G6–G10 → transfers/matching; G14 →
  commissions; G15 → agent wallets; G16 → reputation stats

## 3. Mobile PWA architecture

```
src/
  api/            fetch client + per-domain modules (auth, account,
                  notification, enrollment, ledger, file)
  store/          auth (tokens, me), notifications (inbox + badge),
                  enrollment, ledger
  pages/
    auth/         role-choice, phone, otp, create-account (C1–C4)
    client/       home, history, beneficiaries, profile + flows
    agent/        enrollment (G1–G5), dashboard, balance, profile
  components/ui/  StatusChip, AmountText, PrimaryButton, ListRow,
                  UploadDropzone, OtpBoxes, TabBar, PushedHeader …
  hooks/          shared non-component logic used by 2+ pages
                  (useQuote, useTransfer, useCountdown) — added in
                  slice 7; the alternative was sibling pages importing
                  each other's internals
  sw.ts           service worker: app-shell cache + push handlers
```

- **API client**: one `request()` wrapper — base URL from env, JSON
  bodies in proto3-JSON shape (enums as full names, int64 as strings —
  helpers `parseAmount`/`formatCDF` own the string→bigint edge), uniform
  `{code, message}` error type, bearer token attach, single-flight
  **401→refresh→retry** (refresh needs `refresh_token` + the access
  token's `sub` claim — decode the JWT payload client-side).
- **Session**: tokens in `localStorage` (v1 pragmatism; XSS surface noted
  — no third-party scripts in the PWA), `me` re-fetched on boot; route
  guards per role (`hoc/RequireAuth`, `RequireRole`).
- **PWA** via `vite-plugin-pwa`: manifest (name, `#103D2C` theme,
  maskable icons), app-shell precache only — **no offline mutations,
  money flows require network**; an offline banner, not queued writes.
- **Web push**: SW `push` handler shows `{title, body}` and
  `notificationclick` deep-links via `data`; subscribe flow = get
  vapid-key → `PushManager.subscribe` → `POST /v1/push/subscriptions`,
  triggered from a settings toggle + after first login (permission
  prompt only on gesture). Unsubscribe on logout.
- **Design tokens** → Tailwind v4 `@theme` (the §Design-Tokens table
  verbatim: greens, inks, chips, radii, Schibsted/Hanken via
  self-hosted fonts — PWA must not fetch Google Fonts at runtime).
- **i18n**: FR strings centralized (`data/strings.ts`); `preferred_language`
  synced to the backend but v1 copy is FR-only (per the handoff).

## 4. Proof uploads from the browser

The presign+confirm flow end to end: `CreateUpload` (gateway) → **PUT the
bytes straight to MinIO/S3** from the browser → `ConfirmUpload`. Two
infra consequences:
- **MinIO CORS** must allow PUT from the app origins (compose init job
  extension) — real S3 gets a bucket CORS policy later.
- The dropzone component owns the 3-step dance + progress and returns a
  `file_id` the flows attach (G3 deposit proof → ledger UpdateStatus is
  backend-side via the file event; the UI only needs the upload).

## 5. Gateway additions this plan needs **[C, sequenced behind M's engine]**

- File routes (file doc slice 5): `POST /v1/files` (CreateUpload),
  `POST /v1/files/{id}/confirm`, `GET /v1/files/{id}` — authed tier.
- Ledger routes once LedgerService registers: agent-facing `GET
  /v1/agent/profile`, `GET /v1/agent/movements` (lines), `POST
  /v1/agent/deposits` (funding request), `POST /v1/agent/withdrawals`;
  ops `GET /v1/ledger/requests?status=`, `POST
  /v1/ledger/requests/{id}/approve|reject`.
- CORS: gateway `CORS_ALLOWED_ORIGINS` gains the two app origins (dev:
  vite ports; prod: app domains).

## 6. Admin app (`mokapay-admin`)

Same stack minus PWA. Three v1 sections, all against existing/§5 routes:
1. **Comptes** — search/filter accounts, moderation (role/status), delete.
2. **Enrôlements** — the worklist (`status=accepted` default), promote
   from an account page, approve/cancel with the §6-of-ledger call
   ordering surfaced as one "Approve" action (the backend orchestrates).
3. **Ledger** — pending-approval queue, request detail with proof link,
   approve/reject with reason; profile lookup with balance/held/lines.

Auth: same login flow; `RequireRole(ops|admin)` at the router root —
non-ops get a dead end by design.

## 7. Repo & tooling mechanics

- `mokapay-admin` scaffolded as its own repo, submodule under `app/ui/`
  (mirrors mokapay-mobile). Root Makefile gains `mobile/%` and `admin/%`
  delegations (`dev`, `build`, `typecheck`, `lint`) per the frontend
  verification rules.
- Env: `VITE_API_URL` per app (`http://localhost:8080` dev / compose
  `8085`).
- e2e stays out of v1 (roadmap v2), but each slice ends with a manual
  smoke script in the PR/plan notes.

## 8. Open decisions

| # | Question | Proposal |
|---|---|---|
| F1 | One PWA or two (client vs agent)? | One — shared brand/components, role-gated tabs. C1 was the pre-login role chooser; slice 12 made it the welcome screen (login / create account) since both roles share one login |
| F2 | Token storage | localStorage + refresh rotation for v1; httpOnly-cookie BFF is a later hardening |
| F3 | Where does the TOS text live | Static in the app (versioned string constant matching `TOS_VERSION`) until a CMS need appears |
| F4 | Admin design | No bundle: Tailwind + the same tokens, table-first; invest in design only if ops outgrows it |
| F5 | Redux vs lighter (zustand/query) | Workspace rule says Redux (`store/<domain>`) — keep it; server-cache discipline via thin api modules, no react-query in v1 |

## Slices & Progress

- [x] Slice 1 — PWA foundation **[C]** — done 2026-07-25 (in the
      mokapay-mobile repo, uncommitted). Registre tokens as Tailwind v4
      @theme + self-hosted fonts (@fontsource); api client with uniform
      ApiError, localStorage session, JWT sub decode, single-flight
      401→refresh→retry and amount helpers (int64-string → fr-FR
      formatting); auth store (booting/anonymous/authenticated +
      bootstrap thunk); routes in src/app/app.tsx with
      RequireAuth/RequireRole (role-gated /home vs /agent); PWA via
      vite-plugin-pwa injectManifest — own sw.ts with push +
      notificationclick handlers, manifest (#103D2C, FR), placeholder
      icons (real artwork TODO); ui kit: StatusChip (6 handoff
      variants), Button (primary/ghost/danger), AmountText, ListRow,
      FlowScreen; FR strings centralized. Root make gains mobile/%
      (dev/build/typecheck/lint). Verified: build + tsc + oxlint clean
      (design/ excluded), built PWA serves index/manifest/sw via
      preview.
- [x] Slice 2 — auth flows **[C, M validates D1 delta]** — done
      2026-07-25. Login (C2 phone+password → C3 OTP → role-routed home),
      Register (C2 → C3 → C4 with names/language/password — the OTP is
      collected at C3 but verified atomically with account creation, so
      a bad code bounces back one step keeping the details), Reset
      (phone → OTP → new password). Shared OtpStep (6 boxes over one
      input — paste & OS autofill work; 60s resend countdown),
      PhoneField (+243 pill, E.164 assembly), TextField. Backend
      errors never surface raw: frError maps codes to FR labels.
      Verified: typecheck/lint/build clean; dev app + gateway + user
      service smoke — CORS preflight from the app origin passes and the
      exact OTP call the Login page makes returns 200. Full in-browser
      register needs a throwaway phone (real Cognito user) — M's call.
- [x] Slice 3 — notifications & profile **[C]** — done 2026-07-25.
      AppShell with the role-driven tab bars (client Accueil/Historique/
      Bénéficiaires/Profil, agent Tableau/Transferts/Solde/Profil;
      unbuilt tabs → ComingSoon) loading the inbox on mount so the bell
      badge is right from first paint; C18/G17 feed (tap-to-read,
      mark-all, fr-FR timestamps, tinted icon tiles); C19 profil
      (identity + language edit via PATCH sending only changed fields,
      push toggle, change-password → reset flow, red logout that
      revokes server-side, unsubscribes push and clears locally even if
      revocation fails offline). Push plumbing: vapid-key fetch (empty
      key = dev mode, toggle stays off), permission on gesture only,
      PushManager subscribe → backend registration, unsubscribe both
      sides. Typecheck/lint/build clean. Real-device push test awaits
      M's VAPID keys (notification doc slice 4).
- [x] Slice 4 — agent enrollment **[C]** — done 2026-07-25. One
      `/enrollment` route, status-driven: invited → G1 (scrollable TOS
      v1.0 draft copy + checkbox + accept/decline; TOS_VERSION constant
      must track the backend env), accepted → G4 waiting (ref +
      refresh re-reading enrollment AND account — approval flips the
      role), approved → G5 (bootstrap + dashboard redirect),
      declined/cancelled → terminal card. A pending_agent sees the
      invitation banner on the client home (green card, G1 entry).
      G2/G3 (guarantee funding + deposit proof) slot into the accepted
      state once the ledger/file gateway routes land (slice 5).
      Typecheck/lint/build clean.
- [x] Slice 5 — done 2026-07-25. LEDGER routes (agent
      create/list/statement pinned to own profile; ops
      worklist/approve/reject/statement/profile lifecycle) and FILE
      routes (presign create → browser PUT → confirm → get/list, ops
      reject) with MinIO CORS verified by a live preflight. Every G2–G4
      mobile screen and the admin Ledger tab are unblocked; proof
      uploads work end to end from a browser.
      MinIO CORS, then G2–G4 + G11–G13 screens
- [x] Slice 6 — admin app **[C]** — done 2026-07-25. New repo
      github.com/Mahamadou828/mokapay-admin (pushed), submodule at
      app/ui/mokapay-admin + `make admin/%`. Ops login (phone+password →
      OTP), RequireOps at the router root (non-ops = dead end). Comptes:
      role/status/exact-phone filters, suspend/reactivate/delete
      (confirmed), promote-to-agent on active users. Enrôlements:
      worklist opening on the "À approuver" (accepted) queue with TOS
      version+date shown, approve/cancel. Ledger: placeholder pending
      slice 5. Shares the mobile API client verbatim; Registre palette,
      system fonts, table-first (F4). Typecheck/lint/build clean,
      preview serves. Submodule addition in the main repo left
      uncommitted.
- [x] Slice 7 — transfers phase **[C]** — done 2026-07-26. The transfer
      engine landed, so this covers C6–C17 and G6–G10, G14–G16.
      **Client**: C6 accueil (live rate from a 1 USD quote, send CTA,
      recents), C7 simulateur (debounced quote, green "reçoit" panel),
      C8+C9 as one `/send` route (amount → saved-or-new recipient →
      operators → review; back keeps the draft), and C10–C15 behind one
      status-driven `/transfers/:id` the way the enrollment flow works —
      Searching (radar + elapsed + cancel), Matched (pay details,
      copy-to-clipboard), Tracking (the real event log as the stepper),
      Confirm (releases the agent's hold), Detail (+ `/detail` pins C15
      from anywhere). C16 historique with filter pills, C17 bénéficiaires
      (list + delete; creation is a side effect of the first transfer).
      **Agent**: G6 tableau (guarantee panel, pending-commission tile,
      offer inbox, active queue), G7 offre (server-window countdown,
      accept = the race → not_found reads as "offer gone"), G8 queue with
      an "À faire" count, G9 exécution (client proof thumbnail + payout
      card), G14 commissions, G15 portefeuilles (add/remove, duplicate
      pair caught client-side), G16 folded into the Profil tab above C19
      rather than a second profile. **Proof uploads**: one
      `/transfers/:id/proof` screen for C12/G10/the optional C14 receipt —
      purpose derives from the status and the viewer, never a query param
      — owning presign → PUT (XHR, progress) → confirm, then waiting for
      the proof worker to move the transfer before navigating.
      New deltas D7–D14 recorded above; the CDF scale bug in `formatMinor`
      (a flat ÷100) is fixed — it would have shown every CDF amount 100×
      too small.
      Verified: typecheck/lint/build clean; live against the compose
      gateway (`:8085`) — login, `/v1/me`, operators, quote (1 USD and
      100 USD), CreateTransfer (→ `pending_match` → `pending_agent_approval`
      "offered to 5 agent(s)"), events, auto-saved beneficiary, list, and
      the full file chain create → PUT → confirm (STORED + sha256) → get →
      list. Agent routes confirmed present and role-gated (403 for a
      `user`, 404 for an unknown path). Two environment issues found, not
      fixed — see below.
- [x] Slice 8 — ledger screens **[C]** — done 2026-07-27. G11 Solde
      (green panel + the request feed, `balance_after` joined from the
      statement), G12 Ajouter des fonds (amount + channel + sender number →
      creates the request → instructions), G13 Retirer (real ceiling,
      wallet picker, admin-validation warning), and G2/G3 as one
      `/agent/funding/:id` route the enrollment `accepted` state links to —
      instructions then proof, shared by the guarantee and a self-served
      top-up. `ComingSoon` is gone: every tab now routes to a real screen.
      New deltas D15–D24. Also fixed a slice-7 bug found here: agent
      amounts formatted as NaN because `AgentProfile.currency` is `"USD"`,
      not `CURRENCY_USD` (D24).
      Verified end to end against the compose gateway with a freshly
      enrolled agent: promote → accept (profile created, floor 2000) →
      ops open DEP-2026-000258 → deposit proof presigned to real S3, PUT,
      confirmed → worker moved it to pending_admin_approval with the proof
      attached → enrollment approved → balance 2000, role agent → G12
      top-up DEP-2026-000259 (proof → approve → balance 2500) → G13
      withdrawal WDR-2026-000260 rejected at 600, accepted at 300, held
      300 / available 2200. Two backend items reported, not worked around
      — see below.

- [x] Slice 9 — admin ledger section **[C]** — done 2026-07-27. Closes
      §6.3, the last placeholder in `mokapay-admin`. **Queue** opening on
      "À valider" (`pending_admin_approval`) with "Attente agent",
      "Validées", "Rejetées", "Toutes" and a type filter: ref, type,
      signed amount, status, the declared channel (`reason`), whether a
      proof is attached, date, and inline Valider/Rejeter. Approve
      confirms with the amount spelled out (it moves money and cannot be
      undone); reject demands a reason, which is what the agent is told
      and what releases an outflow's reservation. **Detail**
      (`/ledger/:id`): the money, the reference linkage, the declared
      channel, the proof rendered inline from its presigned URL with the
      sha256 beside it, and the owning profile — balance/held/available/
      guarantee with its settlement lines and activate/suspend.
      **Ouvrir la garantie** on the Enrôlements worklist: resolves the
      profile, refuses to double-open (the engine allows one live request
      per entity), defaults the amount to that profile's own
      `guarantee_floor`. Until this existed no UI could open a guarantee,
      so no agent could pass G4 without curl.
      Also: the admin app now targets `:8085` like the mobile one, and its
      `Money` helper carries the per-currency scale rather than a flat
      ÷100 (the D7 lesson — `LedgerRequest` has no currency of its own, so
      amounts format with the owning profile's).
      Verified against the live gateway: profile-by-account, the
      `agent_profile_id` filter (0 → 1 after opening), createFunding
      (DEP-2026-000263), the duplicate guard (`already_exists`), ops
      reading a proof file it does not own (200 + a working presigned
      URL). Typecheck/lint/build clean; every new module serves.
## Environment issues (slice 7 found them, slice 8 fixed them)

1. **Presigned URLs were unusable from a browser** — compose baked
   `http://minio:9000` into `upload_url`/`download_url`. **Fixed**: object
   storage is real S3 (`mokapay-dev`, eu-west-1, virtual-hosted URLs) and
   compose no longer overrides `S3_ENDPOINT` — the endpoint the service
   signs with is the one the browser is handed. The bucket already carries
   a CORS rule allowing PUT/GET. MinIO stays in the compose file, unwired,
   for offline experiments. Verified: presign → PUT → confirm → presigned
   GET, all from the host.
2. **`.env.example` pointed at `:8080`**, a gateway whose config predates
   the payment and file services. **Fixed**: the example and the client
   fallback target `:8085` (compose), and the gateway's `.env`/`.env.example`
   now spell out `PAYMENT_GRPC_PORT` and `FILE_GRPC_PORT`.
3. **Enrollment accept was broken inside compose** (found in slice 8) — the
   `user` service dialled `localhost:50053` for payment because compose
   never overrode it, so `CreateAgentProfile` failed and no candidate could
   accept. **Fixed**: `PAYMENT_GRPC_PORT: payment:50053` on the user
   service. No `depends_on` — payment already depends on user, and the
   gRPC client dials lazily.

- [x] Slice 10 — corridor choice **[C]** — done 2026-07-27. C7/C8 get the
      two-card converter: "Vous envoyez" over "Le destinataire reçoit",
      a currency pill on each and the swap control between them. The swap
      switches which card is editable — the engine's new reverse quote
      answers "the recipient must get Y, what do I send?" — so both cards
      are real amount fields and the other one always shows the server's
      figure. The corridor rides on the C8 draft, so C9 books what was
      quoted and the back step keeps it. New deltas D25, D26.
      Also fixed: G15 was broken against the reworked wallet contract
      (D26) — it still sent the retired `currency` field and every
      declaration failed with `internal error`.
      Verified live: all four pairs quoted (cdf→usd correctly refused),
      both quote directions (400 USD → 1 126 340 CDF; 1 117 200 CDF →
      396,76 USD), usd→usd and cdf→cdf transfers created and matched
      ("offered to 1 agent(s)"), wallet declared with both flags, neither
      flag refused, duplicate operator refused.

- [x] Slice 11 — the offer inbox on G8 **[C]** — done 2026-07-28. G7
      (accept/refuse) was already built, but its only entry point was the
      G6 dashboard section, fetched once at mount. With `OFFER_TTL` at two
      minutes that inbox is empty far more often than the agent has no
      work — the offer either arrives after the mount or lapses before it,
      and G8, the screen an agent actually opens to look for work, showed
      no offers at all. Now: live offers are pinned above G8's filter
      pills (a two-minute window cannot hide behind a tab, while the queue
      below keeps), `OfferCard` moved to `components/ui/` for its second
      consumer, and both G6 and G8 read the inbox through
      `hooks/useOfferInbox` — one place that re-reads every 10 s and drops
      lapsed offers via `liveOffers`. That filter matters: the inbox query
      keys on status alone and a row only flips to `expired` when the
      engine's sweep job runs, so a just-lapsed offer still comes back as
      `OFFERED` and would advertise work any accept refuses.
      Verified live: a matching transfer (MKP-2026-000164, usd→usd,
      m_pesa payout) produced an offer that reached the running app, was
      opened from the new section and declined from G7 — the decline
      landed at the gateway and the transfer requeued; a second
      (MKP-2026-000165) confirmed the 10 s poll picking up an offer
      created after mount, then lapsing out of the list on its own.

- [x] Slice 12 — C1 rebuilt **[C]** — done 2026-07-28, from a new mock
      (not in the handoff bundle). Logo top-left under a square cluster
      dissolving out of the top-right corner, pitch and CTAs
      bottom-anchored, legal line last. **The role choice is gone**: both
      roles authenticate through the same phone + password + OTP and the
      landing app comes from `/v1/me`, so "je suis un utilisateur / je
      suis un agent" was a decision with no consequence — and a wrong tap
      read as a dead end. It is now **Se connecter** → `/login` and
      **Créer un compte** → `/register`. The `?as=agent` query the old
      screen wrote was never read by anything; dropped with it.
      Decorations are CSS boxes + one SVG (`ui/Decor.tsx`), no raster
      assets: flat colour and straight edges stay sharp at every density
      and need no @2x/@3x. Two things the mock leaves open — the logo is
      a dashed placeholder there, so the screen keeps the MOKA PAY
      wordmark until a real asset lands; and the legal line renders as
      plain text, since the app ships no terms/privacy page to link to
      (see F3 — the TOS text is a versioned constant).

- [x] Slice 13 — the client header **[C]** — done 2026-07-28. Green
      (`bg-primary`) header on the client tabs: avatar left (initials on
      tint, tapping opens Profil), bell right, the C1 square motif in
      tint/25 behind them. The greeting of the mock is dropped as asked.
      **It lives in `AppShell`, not in the pages** — that is the actual
      fix for "the header disappears on Historique / Bénéficiaires": each
      page owned its own header, so only C6 had one. Now one header
      renders above the `<Outlet />` and survives tab navigation, and C6's
      own wordmark+bell row is gone (the bell moved into it).
      Sticky, not fixed: the tab bar is already fixed and a second fixed
      layer would make every page reserve space for it; sticky keeps the
      header in flow and costs the pages nothing. Its top padding carries
      `env(safe-area-inset-top)` so the green runs behind the status bar
      when installed — the manifest `theme_color` is already this green,
      so the two meet without a seam. The bell gained a `tone="light"`
      variant, with the unread dot on `accent` there: the red dot on dark
      green reads as an error rather than as "du nouveau".
      The agent tabs deliberately keep their per-screen headers — G6
      carries the level chip, G8/G11 their titles — so the green header is
      client-only until the agent frames get the same treatment.

- [x] Slice 14 — the rate block **[C]** — done 2026-07-28. C6's rate stops
      being a label-left/value-right row and becomes the stacked block of
      the mock: small grey label, the rate as the hero figure under it
      (26px, tabular). Still the tap target for C7. The graph button of
      the mock is deliberately left out: quoting is stateless, so there is
      no rate history to plot — exactly one point exists at any time, and
      a sparkline would have to be invented. Storing a daily series is a
      backend decision, not something the app should fake.
      The rate's fetch moved with it into `ui/RateBlock.tsx`, which put
      C6 back under the 60-line page budget (93 → 66).

- [x] Slice 15 — "Renvoyer" shelf **[C]** — done 2026-07-28. Under C6's
      recent transfers: a horizontal snapping row of cards (avatar, name,
      masked number + operator, last amount) with **Renvoyer** and
      **Modifier**, plus a × that dismisses the suggestion. Needed no new
      endpoint — the suggestions are the client's own transfer list,
      deduped by `recipient_phone` (not `beneficiary_id`, which is absent
      until the backend saves one, so two sends to the same number would
      show twice).
      Both buttons open the same seeded draft and differ only in where
      they land: Renvoyer goes straight to C9 with the previous amount,
      Modifier stops on C8. **Neither books anything on tap** — the client
      always confirms on C9 against a re-priced quote, so a repeat is a
      starting point, never a booked instruction.
      To carry a whole recipient the send route's navigation state became
      `SendSeed { draft?: Partial<Draft>, startAt? }`; C7 was moved onto
      it. `startAt: "review"` is honoured only when the seed is complete
      (`recipientReady`, extracted from C8's button rule and now shared),
      and C9 shows a loading line instead of nothing while the seeded
      quote is in flight. Dismissals are device-local (localStorage): the
      backend has no hidden-suggestion concept and the card returning on
      another device beats inventing a write per × tap.

- [x] Slice 16 — C17 rebuilt + C17b **[C]** — done 2026-07-28. The book is
      now: title, an **Ajouter** pill, a three-up **Récents** shelf, then
      **Tous les bénéficiaires** with a local search (name or number,
      accent-insensitive) and chevron rows. Rows open a new detail screen
      at `/beneficiaries/:id` — send / modify / delete — and `Ajouter`
      opens `/beneficiaries/new`. Both are pushed screens sharing one
      `ui/BeneficiaryForm`.
      "Récents" is ordered from the *transfers*, not the beneficiary rows:
      the list arrives by `created_at` and `updated_at` only moves when a
      transfer carried inline details, so sending to an already-saved
      recipient leaves its row untouched. C17 therefore fetches transfers
      alongside recipients.
      **On editing** — there is no `UpdateBeneficiary` RPC (List / Add /
      Delete only), but `add_beneficiary` upserts on the live
      `(client_nk, phone)` pair, so posting a saved number refreshes its
      name / operator / city on the same row and id. That is what the edit
      form does; two limits follow from the key and are surfaced in the UI:
      the phone is read-only when editing (a new number is a new
      recipient), and the procedure `COALESCE`s the city, so a city can be
      changed but not cleared. Changing a number needs a real update RPC —
      raised with [M], not worked around.
      The mock's "Upload" (bulk import) is left out: no endpoint, and
      inventing a CSV path would be a bigger feature than it looks.
      The delete now awaits its thunk before navigating — the row only
      leaves the store on success, so the previous fire-and-forget would
      have reported a delete that did not happen.

- [x] Slice 17 — `UpdateBeneficiary` wired **[C]** — done 2026-07-28,
      against the RPC [M] added (`PATCH /v1/beneficiaries/{id}`, partial,
      409 on a phone clash). The edit form drops the read-only phone and
      its "ne peut pas être modifié" hint — those only existed because the
      add-upsert keyed on the number — and now sends **only the fields
      that changed**: the procedure COALESCEs, so sending everything would
      be harmless but would let an untouched field overwrite a concurrent
      edit. Save stays disabled until something actually differs.
      A clash surfaces as the server's `already_exists`, which the error
      table already renders as "Ce numéro est déjà enregistré." Add and
      edit share one store matcher (add is still an upsert, so it can come
      back with a known id). Still open in the SQL, not the app: a city can
      be changed but not cleared — clearing needs a sentinel in
      `update_beneficiary`.

- [x] Slice 18 — G6 header **[C]** — done 2026-07-28. The greeting row and
      the guarantee panel merge into one full-bleed green block (behind the
      status bar, rounded bottom), bell on the right, level as text rather
      than a chip. **The mock's progress bar is replaced by the two
      figures** — Bloqué and Disponible — because a bar needs a ceiling and
      a guarantee has none: only `guarantee_floor`, a minimum. Any bar here
      would have to invent its 100 %.
      The square motif moved to `components/ui/CornerSquares` (second
      consumer). The header takes `loaded` so the pre-read null profile
      shows "Chargement…" rather than accusing a healthy account of being
      inactive.
      Then promoted to the shell in the same slice: `components/layout/
      AgentHeader`, greeting replaced by the avatar (→ Profil) like the
      client's, square bottom. **Not sticky**, unlike the client bar — at
      ~150px, pinning it would hold a third of the screen while an agent
      scrolls a queue. Consequences, all deliberate: the shell now owns the
      agent profile read (G6 and G11 dropped theirs), G11 lost its money
      panel (the header states the same three figures — showing them twice
      on one screen is how they end up disagreeing), and `BalancePanel`
      lost its last consumer and was deleted.

- [x] Slice 19 — G8 cards **[C]** — done 2026-07-28. The queue rows became
      the mock's cards: reference + recipient over the status chip, then the
      two sides of the deal — what the agent collects on the left, what the
      beneficiary is owed (green) on the right. The left figure is
      `total_amount`, not `from_amount`: the client hands over amount + fee,
      and that total is what the agent takes in (the same figure G7 calls
      "Le client vous verse").
      Its label is `collectionLabel(status)` — "Reçu" once the transfer is
      past the client's payment, "À recevoir" before, and **nothing** for a
      cancelled or expired transfer, where neither is true. The mock's "Reçu
      de A" drops the "de A": `Transfer` carries `client_id` but no client
      name, and inventing one on a money row is not on.
      The **À faire** pill is gone, replaced by **Tous** (now the default):
      every card states whose move it is, so the filter only hid the rest of
      the work. `needsAction` went with it (its only caller) and
      `AgentTransferRow` lost its now-unused `card` variant — G6 keeps the
      compact rows.

- [x] Slice 20 — offer cards **[C]** — done 2026-07-28. Offers adopt the
      "Renvoyer" card shape and shelf (`components/ui/OfferShelf`, now used
      by both G6 and G8 so they cannot drift; the page-local `OfferInbox`
      is gone). One button — **Ouvrir** → G7 — because accepting needs the
      hold and the full breakdown, which do not fit a 262px card.
      Two slots differ from the send-again card, deliberately: the
      countdown takes the place of the dismiss ×, since an offer's × would
      be *refuser* — a real, irreversible answer to the client, not a local
      "hide this" — and the avatar carries the payout operator badge
      instead of initials, because an offer names nobody (the agent learns
      who the beneficiary is on accepting).

- [x] Slice 21 — counterpart proofs via the party-scoped read **[C]** —
      done 2026-07-28, per [M]: `GET /v1/files/{id}` is owner-only, so
      showing the *other* party's proof needs
      `GET /v1/transfers/{id}/files` (client, assigned agent and ops; a
      transfer that isn't yours reads 404). Switched: G9's inline view of
      the client's payment proof, and C15's two proof buttons.
      C15 reads **both** proofs through it, including the client's own —
      one code path beats branching on whose upload it was. Entries are
      matched on `file.id`, not purpose: a rejected proof stays attached
      and another is uploaded after it, so a transfer can carry several
      files of one purpose and the id on the transfer is the current one.
      The list already carries presigned URLs, so it needs no second call.
      `fileApi.get` stays for own uploads; the admin app is untouched (it
      only reads deposit proofs, which are owner + ops-tier anyway).
      Then closed in the same slice: **C14 shows the agent's payout proof**,
      on the screen where the client decides rather than one tap away on
      C15. Confirming is what releases the agent's hold, so the evidence
      belongs next to the yes; it was absent before only because the client
      could not read the agent's file.
      The viewer is `components/ui/ProofCard`, shared by G9 and C14: a
      bordered card carrying what the proof is, a chip for whether it
      exists, the party and the figure it evidences, and one link to open
      it. No thumbnail, which also means **nothing is fetched until the
      tap** — the chip answers "is there a proof?" from the id already on
      the transfer, and a presigned URL minted at render would be stale by
      the time anyone used it. A file that is attached but unreadable
      (pending, rejected) reports "Preuve indisponible" rather than opening
      a blank tab.
      The party line differs by side out of necessity: C14 names the
      beneficiary and their operator, G9 can only name the channel the
      client paid from — `Transfer` carries `client_id` but no sender name,
      the same gap as the G8 card. `agent.clientProofOf` and `proof.open`
      died with the thumbnail and were removed from the strings table.
      C15 followed: its two compact buttons became two stacked cards
      (side by side, each card's party line and figure would truncate to
      nothing), and `ProofLink` is gone — all three proof surfaces are one
      component now.

- [x] Bug — the funding proof screen hung on "Envoi…" **[C]** — fixed
      2026-07-28, reported from a session where every request returned 200.
      Confirming an upload only *announces* the file; a worker then forwards
      the ledger request to admin approval. G3 re-read the request once on
      success and raced that worker: the read came back still
      `pending_agent_action`, so `awaitsAgent` kept the screen on the proof
      step, whose button never leaves "Envoi…" (it clears on failure only).
      No error, nothing retryable — it just sat there.
      Fixed on both sides: `Funding` tracks a local `sent` and shows the
      waiting panel immediately (the agent is done regardless of what the
      worker has got to), then polls every 3 s until the status actually
      leaves `pending_agent_action`; `DepositProof` clears its own busy flag
      on success rather than depending on the parent to unmount it.
      The transfer-proof screen never had this — it already waits on a
      bounded `settle()` poll before navigating. Only the ledger path was
      missing one.

- [x] Bug — safe-area insets were inert **[C]** — fixed 2026-07-28. The two
      green headers padded their top with `env(safe-area-inset-top)`, but
      the viewport meta lacked `viewport-fit=cover`, so every inset
      resolved to 0: the installed app was letterboxed and the green never
      reached behind the status bar as slice 13 claimed.
      Adding `viewport-fit=cover` opts the whole app into the unsafe areas,
      so it came with the padding every edge-touching surface then needs:
      `FlowScreen` (top, or the back arrow sits under the status bar;
      bottom, or the CTA each flow parks at `mt-auto` lands under the home
      indicator), the tab bar and the content reserve above it (both grow
      by the same inset so they cannot disagree), and C1, which is outside
      the shell. The design's paddings stay the floor via `max()`, so
      nothing moves in a browser tab where the insets are 0.
      Verified in the built CSS — arbitrary `env()` values survive
      Tailwind: six emitted rules, and `dist/index.html` carries the meta.
      Left/right insets are skipped: the `max-w-md` column is centred, and
      a landscape phone is wider than 448px + insets, so it never reaches
      them.

## Backend items found in slice 8 (fixed by [M], re-verified)

1. **A `pending_agent` could not read their own ledger requests** — the
   gateway agent tier admits the role but payment's
   `ListLedgerRequests`/`ListLedgerLines` did not, which left G2 unable to
   discover its guarantee request. **Fixed**: both role lists accept
   `pending_agent` and `isAgent()` treats it as an agent for the own-profile
   pin. Re-verified with a fresh candidate: both routes 200, the
   enrollment-referenced funding is visible, and the response contains that
   candidate's profile id and no other.
2. **Agent withdrawals were stranded** at `pending_agent_action`, invisible
   to the ops worklist and unapprovable, holding their reservation forever.
   **Fixed**: the `case "agent"` arm branches on type — funding →
   `pending_agent_action` (it owes a proof), withdrawal →
   `pending_admin_approval`. Re-verified: WDR-2026-000261 created at
   pending_admin_approval, present in the worklist, approved, balance
   2500 → 2400. The one request stranded before the fix
   (WDR-2026-000260) was rejected to release its 300 reservation.

Full re-run after both fixes, with a second candidate taken from scratch:
promote → accept → ledger reads as `pending_agent` → ops open
DEP-2026-000262 → deposit channels → proof presigned to S3, PUT, confirmed
→ worker moved it to pending_admin_approval with the proof attached →
enrollment approved → profile active, balance 2000, role agent.
