// Smoke: the journey helpers that decide a replay's inputs, without a browser.
import { makeData, makeBp, money, StepFailed, WaitReached, cleanError } from '../bp-journey-lib.mjs';

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
console.log('ok journey lib');
