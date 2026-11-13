// Smoke: the journey helpers that decide a replay's inputs, without a browser.
import { makeData, makeBp, money, StepFailed, WaitReached, cleanError } from '../bp-journey-lib.mjs';
import { edgeReplay } from '../bp-data.mjs';

const fail = (m) => { console.error('FAIL ' + m); process.exit(1); };
const draw = (seed) => {
  const inputs = {};
  const d = makeData(seed, inputs);
  d.int(1, 6); d.pick(['a', 'b', 'c']); d.int(10, 10); d.plusEmail('info+megan@shop.test');
  return inputs;
};
const a = draw(42), b = draw(42), c = draw(43);
if (JSON.stringify(a) !== JSON.stringify(b)) fail('same seed, different inputs');
if (JSON.stringify(a) === JSON.stringify(c)) fail('different seeds, same inputs');
if (Object.keys(a).join() !== 'int1,pick1,int2,plusEmail1' || a.int2 !== 10) fail('inputs ' + JSON.stringify(a));
if (a.plusEmail1 !== 'info+megan-42@shop.test') fail('plusEmail ' + a.plusEmail1);
if (makeData(5, {}).plusEmail('ann@shop.test') !== 'ann+5@shop.test') fail('plusEmail without a tag');
for (const bad of [() => makeData(1, {}).int(3, 1), () => makeData(1, {}).pick([]), () => makeData(1, {}).plusEmail('nope')]) {
  try { bad(); fail('bad input accepted'); } catch (e) { if (e.message.startsWith('FAIL')) throw e; }
}
if (money(1234.5) !== '£1,234.50' || money(0) !== '£0.00') fail('money ' + money(1234.5));

let shot = 0;
const bp = makeBp({ onFail: async () => { shot++; } });
await bp.step('one', async () => {});
try {
  await bp.step('two', async () => { await bp.step('inner', async () => { throw new Error('boom\nCall log:\n  - waiting'); }); });
  fail('step error swallowed');
} catch (e) {
  if (!(e instanceof StepFailed) || e.step !== 3 || e.stepName !== 'inner' || shot !== 1) fail(`step ${e.step} ${e.stepName} shots ${shot}`);
  if (cleanError(e.message) !== 'boom') fail('cleanError ' + cleanError(e.message));
}
// Waits: the first run stops at the first wait; a resumed one skips the
// steps before it and gets the arrival.
const ran = [];
const journey = async (b) => {
  await b.step('sign up', async () => { ran.push(1); });
  const mail = await b.waitFor({ email: { subject: 'Confirm' }, within: '2h' });
  await b.step('confirm', async () => { ran.push(2); return mail.link(/confirm/); });
  await b.waitFor({ time: { after: '1m' } });
  await b.step('come back', async () => { ran.push(3); });
};
try {
  await journey(makeBp());
  fail('the first wait did not stop the journey');
} catch (e) {
  if (!(e instanceof WaitReached) || e.n !== 1 || e.step !== 1 || JSON.stringify(e.until) !== '{"email":{"subject":"Confirm"},"timeout":"2h"}') fail('wait 1 ' + JSON.stringify(e));
}
const confirm = { detail: 'email', message: { links: ['https://x.test/help', 'https://x.test/confirm?t=1'] } };
try {
  await journey(makeBp({ resume: { after: 1, arrivals: [confirm] } }));
  fail('the second wait did not stop the journey');
} catch (e) {
  if (!(e instanceof WaitReached) || e.n !== 2 || e.step !== 2 || JSON.stringify(e.until) !== '{"time":{"after":"1m"}}') fail('wait 2 ' + JSON.stringify(e));
}
await journey(makeBp({ resume: { after: 2, arrivals: [confirm, { detail: 'time reached' }] } }));
if (ran.join() !== '1,2,3') fail('steps ran ' + ran.join());
for (const bad of [{ handoff: { role: 'r', story: 's' } }, {}, { email: {} }, { time: {} }, { email: { from: 'a' }, time: { after: '1m' } }]) {
  try { await makeBp().waitFor(bad); fail('bad wait accepted ' + JSON.stringify(bad)); } catch (e) { if (e.message.startsWith('FAIL') || e instanceof WaitReached) fail(JSON.stringify(bad)); }
}
try { await makeBp().waitFor({ handoff: {} }); } catch (e) { if (!/cannot wait for a hand-off/.test(e.message)) fail(e.message); }
try {
  await makeBp({ resume: { after: 1, arrivals: [{ detail: 'x', message: { links: [] } }] } }).waitFor({ email: { from: 'a' } }).then((m) => m.link(/nope/));
  fail('link found nothing and did not throw');
} catch (e) { if (!/no link matching/.test(e.message)) fail(e.message); }

// Generated data: the same seed draws the same values, with edge cases in
// about 30% of replays, one or two each.
const full = (seed) => {
  const inputs = {};
  const d = makeData(seed, inputs);
  d.personName(); d.businessName({ size: 'firm' }); d.address(); d.phone(); d.vatNumber(); d.companyNumber();
  d.lineItems({ max: 6 }); d.date(); d.dateRange();
  return inputs;
};
if (JSON.stringify(full(42)) !== JSON.stringify(full(42))) fail('generators: same seed, different inputs');
const vatOK = (v) => {
  const m = /^GB(\d{9})$/.exec(v);
  if (!m) return false;
  const d = m[1].split('').map(Number);
  const t = d.slice(0, 7).reduce((s, x, i) => s + x * (8 - i), 0) + Number(m[1].slice(7));
  return t % 97 === 0 || (t + 55) % 97 === 0;
};
const londonToday = new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/London' }).format(new Date());
const dayDiff = (iso) => Math.round((Date.parse(iso + 'T00:00:00Z') - Date.parse(londonToday + 'T00:00:00Z')) / 86400000);
for (let seed = 1; seed <= 300; seed++) {
  const d = makeData(seed, {});
  const ph = d.phone(); if (!/^07700 900\d{3}$/.test(ph)) fail('phone ' + ph);
  const vat = d.vatNumber(); if (!vatOK(vat)) fail('vat ' + vat);
  const cn = d.companyNumber(); if (!/^\d{8}$/.test(cn)) fail('company number ' + cn);
  const dt = d.date({ from: -10, to: 5, edges: false });
  if (!/^\d{4}-\d{2}-\d{2}$/.test(dt) || dayDiff(dt) < -10 || dayDiff(dt) > 5) fail('date ' + dt);
  const a = d.address({ edges: false }); if (!a.line1 || !a.town || !/^[A-Z]{1,2}\d[A-Z\d]? \d[A-Z]{2}$/.test(a.postcode)) fail('address ' + JSON.stringify(a));
  const r = d.dateRange({ edges: false }); if (r.end < r.start) fail('range ' + JSON.stringify(r));
}
let longSeen = false;
for (let seed = 1; seed <= 5000 && !longSeen; seed++) {
  const inputs = {};
  const n = makeData(seed, inputs).businessName({ exclude: ['leading-space', 'line-break'] });
  if ((inputs.edges || []).includes('businessName1:long')) { longSeen = true; if (n.length !== 120) fail('long name is ' + n.length); }
}
if (!longSeen) fail('no long business name in 5000 seeds');
let edgy = 0;
for (let seed = 1; seed <= 2000; seed++) {
  const inputs = {};
  const d = makeData(seed, inputs);
  d.personName(); d.businessName(); d.address(); d.price(); d.quantity(); d.date(); d.dateRange(); d.lineItems({ min: 4, max: 4 });
  const used = (inputs.edges || []).length;
  if (edgeReplay(seed)) { edgy++; if (used < 1 || used > 2) fail(`seed ${seed}: edge replay used ${used} edges`); }
  else if (used !== 0) fail(`seed ${seed}: not an edge replay but used ${used}`);
}
if (edgy < 500 || edgy > 700) fail(`edge replays ${edgy} of 2000`);
let zeroSeed = 0;
for (let seed = 1; seed <= 5000 && !zeroSeed; seed++) {
  const inputs = {};
  makeData(seed, inputs).quantity();
  if ((inputs.edges || []).includes('quantity1:zero')) zeroSeed = seed;
}
if (!zeroSeed) fail('no zero quantity in 5000 seeds');
const seq = (seed, ex) => {
  const inputs = {};
  const d = makeData(seed, inputs);
  d.quantity(ex ? { exclude: ['zero'] } : {}); d.personName(); d.price(); d.lineItems({ min: 3, max: 3 }); d.dateRange(); d.businessName();
  return inputs;
};
const withZero = seq(zeroSeed, false), without = seq(zeroSeed, true);
if (withZero.quantity1 !== 0 || without.quantity1 === 0) fail(`exclude: ${withZero.quantity1} then ${without.quantity1}`);
for (const k of Object.keys(withZero)) {
  if (k === 'edges' || k === 'quantity1') continue;
  if (JSON.stringify(withZero[k]) !== JSON.stringify(without[k])) fail('exclude moved ' + k);
}
for (let seed = 1; seed <= 500; seed++) {
  const inputs = {};
  const d = makeData(seed, inputs);
  for (const g of ['personName', 'businessName', 'address', 'price', 'quantity', 'date', 'dateRange', 'lineItems']) d[g]({ edges: false });
  if (inputs.edges) fail(`edges: false gave ${inputs.edges}`);
}
const hb = makeBp();
if (hb.totalWithVat([{ quantity: 2, price: 10 }], 0.2) !== 24 || hb.totalWithVat([{ quantity: 3, price: 1.1 }]) !== 3.96) fail('totalWithVat');
if (hb.ukDate('2026-02-09') !== '09/02/2026') fail('ukDate ' + hb.ukDate('2026-02-09'));
console.log('ok journey lib');
