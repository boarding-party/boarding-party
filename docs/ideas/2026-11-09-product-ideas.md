# Product ideas, 9 November 2026

Notes from a conversation while the first live lead ran. None of this is
decided or built. Each section says what the idea is, why it matters, and
what is still unknown.

## 1. Is boardingparty a commercial product?

Possibly, but not as "AI agents test your website": that market is crowded
(see section 2) and we would lose on price, setup and noise.

What boardingparty does that scripted tools do not:

- Journeys across roles and days: one persona books, another approves, an
  email arrives hours later. Handoffs, email waits and calendar triggers
  already work.
- Personas that remember: cookies, notes and accounts carry over between
  runs.
- Bugs that do not break a test: wording, unreadable print, misleading
  messages. These came from a persona's judgement.
- The loop: report, fix, check again, and now leads that write the cast.
- It runs in the customer's own cluster.

First market to try: products where several kinds of user depend on each
other over time (booking, healthcare, marketplaces, HR, property). Pitch:
"a cast of synthetic users lives with your product for a week and tells you
where it breaks between them."

Before anyone pays:

1. Measure the cost per useful bug. Use cheaper models for routine stories.
2. A hosted option (section 3). Nobody small installs Kubernetes.
3. A metered Anthropic API key. Runs use a subscription token today; setup
   already warns this is not allowed for automated use.
4. Distribution: sell through trust with founders and engineers.

Cheapest test: run a lead on augmentive or lumistack for a week, count the
real bugs, and show the owners. If they would pay for that report, there is
a product.

## 2. Competitors (quick scan, vendor claims, prices unverified)

Closest: Marketrix AI (launched March 2026): persona-based simulations,
checkout and login flows, runs on pull requests; listed at roughly
$299–2,500 a month.

AI end-to-end testing (about 20 tools): QA.tech (from $624/month),
Momentic (usage-based, around $125/month), Spur (managed, reported
$4,000–8,000/month), Shiplight and Endtest (email and multi-tab flows),
TestMu (formerly LambdaTest), open source Browser Use and Expect. These are
regression tools: plain-English steps checking that a page still works.

Synthetic users for UX research: Synthetic Users, Delve.ai (about $99 per
100 synthetic users), Uxia. Mostly interviews and surveys, no browser. They
own the phrase "synthetic users".

Gap: the scan found no product doing several user accounts in one flow;
Shiplight and Endtest come closest. Unclaimed together: journeys over days,
roles that depend on each other, self-hosted, a lead that writes the cast
and checks fixes. Lead with that, not with "AI testing" or "synthetic
users".

Fidelity is the question buyers will ask (AgentPersonaBench, 2026, tests
whether persona agents act like their persona). Run videos are our answer.

Sources: Marketrix press release (accessnewswire.com), Product Hunt listing
(producthunt.com/products/marketrix-user-testing-agent), Quash roundup
(quashbugs.com/blog/new-ai-testing-tools-emerging), qa.tech/pricing,
bug0.com pages on Momentic and Spur, shiplight.ai, endtest.io,
testmuai.com, browser-use.com, syntheticusers.com, delve.ai,
arxiv.org/html/2610.04379v1.

## 3. Install bundle: k3s on one server, k3d for a laptop

A laptop bundle (k3d or minikube) is fine for a 20-minute demo, but a cast
that waits for email or wakes at 09:00 needs a machine that stays on.

Pick: `boardingparty up` on any small Linux server. It installs k3s, tiny
and the director, then prints the board link.

- One run at a time with Chromium should fit 4–8 GB (about £10–20 a month).
  To measure.
- No public address needed: the director fetches mail over IMAP; the board
  opens through the CLI's port-forward.
- k3s has a network policy controller, so the egress fence should be
  enforced (setup warns today that it may not be). To confirm.
- Each customer brings their own Anthropic key.

Hosted later means one such server per customer, run by us, from the same
installer.

First unknown: does tiny install cleanly on plain k3s (sessions, egress
proxy, network policies)? It has only run on GKE.

## 4. Explore with AI, replay with Playwright

Cut model cost by recording journeys personas find and replaying them as
plain Playwright scripts.

1. Record: a persona finishes a story with no bugs. Playwright MCP reports
   the code for each action, and we keep the trace, so the run becomes a
   script.
2. Parameterise what changes: emails, invoice numbers, dates.
3. Turn what the persona saw into checks ("the total shows …").
4. Replay on the schedule: a browser pod, no model. Waits stay with the
   director: the script runs to "wait for email", the director waits, the
   next part continues.
5. Replay breaks: start an AI run of the same persona with "the script
   broke at step 4". It tells a real bug from a changed page, and records a
   new script when the page changed.

Replay finds regressions only. It never finds new confusion (the footer,
the misleading "All required fields complete", the briefing wording). So it
is a mix: scripts daily or per deploy, AI personas after real changes and
on a slow schedule, and leads choosing where AI time goes.

Hard parts: waits written as "until visible", never sleeps; test data that
piles up in a persona's account; email steps reading the director's mail
store. Positioning: replay alone is what Momentic and QA.tech sell, so it
must stay the cheap layer under the personas.

Unknown: what an AI run costs today. Measure before building, to know if
replay saves 10× or 100×.

## 5. Replays with generated data

The AI writes the script once with data generators, so every replay types
something different. Model cost only at writing time.

- A generator per field: business name, UK address and postcode, VAT
  number, line items, prices, dates.
- Data that fits the persona: a bookkeeper gets long company names and many
  line items; a sole trader gets one item and a round price.
- An edge-case list, one or two mixed into each replay: O'Brien, Zoë Ågren,
  a 120-character company name, £0.01, £999,999.99, quantity 0, 29
  February, a due date before the invoice date, leading spaces, pasted line
  breaks.
- Checks worked out from the inputs (total = sum(qty × price) × 1.2), not
  fixed strings.

Rules:

1. Every replay records its random seed, so a failure replays with exactly
   the same data, and the board shows the inputs that broke it.
2. Data stays inside the persona's limits: emails only as plus-addresses on
   the persona's inbox, inputs only in the story's own forms. On production
   sites the data still lands in real forms.

A failed random input is not automatically a bug (the site may be right to
reject quantity 0). A failed replay goes to an AI run with the inputs and
the error, which reports only when the site misbehaved.
