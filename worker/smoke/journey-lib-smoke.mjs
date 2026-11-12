// Smoke: the journey helpers that decide a replay's inputs, without a browser.
import { makeData, makeBp, money, StepFailed, cleanError } from '../bp-journey-lib.mjs';

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
try { await bp.waitFor({}); fail('waitFor ran'); } catch (e) { if (!/later version/.test(e.message)) fail(e.message); }
console.log('ok journey lib');
