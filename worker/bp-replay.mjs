#!/usr/bin/env node
// bp-replay runs a recorded journey with Playwright and no model.
//
//   bp-replay --journey <file> --self [--seed N] [--expect-fail-step N] [--device KEY]
//     An AI run proving its journey: prints one JSON result line; exits 0
//     only when the result proves the revision (passed, or failed exactly
//     at --expect-fail-step).
//
//   bp-replay --journey-url URL --report-url URL --upload-url URL --profile-url URL
//             [--live-url URL] [--console-url URL] [--seed N] [--device KEY]
//     A replay run: fetches the code, restores the persona's browser, runs,
//     uploads the evidence, saves the browser back and PUTs the result.
//
// The journey itself runs in a child process whose argv and environment
// carry none of these URLs (they hold the run token). Logs go to stderr;
// stdout carries only the result line.
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomInt } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const log = (...a) => console.error('bp-replay:', ...a);
const self = fileURLToPath(import.meta.url);

function parse(argv) {
  const o = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith('--')) continue;
    const eq = a.indexOf('=');
    if (eq > 0) { o[a.slice(2, eq)] = a.slice(eq + 1); continue; }
    const key = a.slice(2);
    if (key === 'self' || key === 'child') { o[key] = true; continue; }
    o[key] = argv[++i];
  }
  return o;
}

const opts = parse(process.argv.slice(2));
if (opts.child) {
  await child();
} else if (opts.self) {
  await selfMode();
} else if (opts['journey-url']) {
  await runMode();
} else {
  log('usage: bp-replay --journey FILE --self | --journey-url URL --report-url URL --upload-url URL --profile-url URL');
  process.exit(2);
}

function seedOf(v) {
  if (v === undefined || v === '') return randomInt(1, 2 ** 31);
  const n = Number(v);
  if (!Number.isSafeInteger(n) || n <= 0) { log('--seed must be a positive whole number'); process.exit(2); }
  return n;
}

// runChild runs the journey in a child process and returns its result. The
// child gets only paths and plain options: no URL, no token.
function runChild({ file, seed, device, outDir, profile, saveProfile, cdp }) {
  const args = [self, '--child', '--file', file, '--seed', String(seed), '--out', outDir];
  if (device) args.push('--device', device);
  if (profile) args.push('--profile', profile);
  if (saveProfile) args.push('--save-profile', saveProfile);
  if (cdp) args.push('--cdp', '1');
  const env = {};
  for (const k of ['PATH', 'HOME', 'LANG', 'LC_ALL', 'PLAYWRIGHT_BROWSERS_PATH', 'HTTPS_PROXY', 'https_proxy', 'NO_PROXY', 'no_proxy', 'BP_REPLAY_TIMEOUT_MS', 'BP_PW_ROOT']) {
    if (process.env[k] !== undefined) env[k] = process.env[k];
  }
  const resultFile = join(outDir, 'result.json');
  args.push('--result', resultFile);
  return new Promise((resolve) => {
    const p = spawn(process.execPath, args, { env, stdio: ['ignore', 'inherit', 'inherit'] });
    p.on('close', (code) => {
      try { resolve(JSON.parse(readFileSync(resultFile, 'utf8'))); } catch {
        resolve({ status: 'failed', step: 0, error: `the journey runner stopped (exit ${code}) without a result`, seed });
      }
    });
  });
}

async function selfMode() {
  if (!opts.journey) { log('--journey FILE is required'); process.exit(2); }
  const seed = seedOf(opts.seed);
  const outDir = mkdtempSync(join(tmpdir(), 'bp-self-'));
  const res = await runChild({ file: opts.journey, seed, device: opts.device, outDir });
  process.stdout.write(JSON.stringify(res) + '\n');
  const want = opts['expect-fail-step'] ? Number(opts['expect-fail-step']) : 0;
  const proven = want ? res.status === 'failed' && res.step === want : res.status === 'passed';
  process.exit(proven ? 0 : 1);
}

async function put(url, body, type) {
  const r = await fetch(url, { method: 'PUT', body, headers: { 'Content-Type': type } });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
}

async function runMode() {
  for (const k of ['report-url', 'upload-url', 'profile-url']) {
    if (!opts[k]) { log(`--${k} is required`); process.exit(2); }
  }
  const seed = seedOf(opts.seed);
  const outDir = mkdtempSync(join(tmpdir(), 'bp-replay-'));
  let res;
  try {
    const r = await fetch(opts['journey-url']);
    if (!r.ok) throw new Error(`could not fetch the journey (HTTP ${r.status})`);
    const file = join(outDir, 'journey.mjs');
    writeFileSync(file, await r.text());
    let profile = '';
    try {
      const pr = await fetch(opts['profile-url']);
      if (pr.status === 200) {
        profile = join(outDir, 'profile.json');
        writeFileSync(profile, Buffer.from(await pr.arrayBuffer()));
      } else if (pr.status !== 204) log(`no profile restored (HTTP ${pr.status})`);
    } catch { log('no profile restored'); }
    // The live screen and the console recorder attach over CDP, as in AI runs.
    if (opts['live-url']) spawn(process.execPath, ['/opt/bp/bp-screen.mjs', `--live-url=${opts['live-url']}`], { stdio: 'ignore', detached: true }).unref();
    if (opts['console-url']) spawn(process.execPath, ['/opt/bp/bp-console.mjs', `--console-url=${opts['console-url']}`], { stdio: 'ignore', detached: true }).unref();
    const saveProfile = join(outDir, 'profile-out.json');
    res = await runChild({ file, seed, device: opts.device, outDir, profile, saveProfile, cdp: true });
    await uploadEvidence(outDir);
    if (existsSync(saveProfile)) {
      await put(opts['profile-url'], readFileSync(saveProfile), 'application/json').catch((e) => log('profile not saved:', e.message));
    }
  } catch (e) {
    res = { status: 'failed', step: 0, error: String(e.message || e).slice(0, 2000), seed };
  }
  try {
    await put(opts['report-url'], JSON.stringify(res), 'application/json');
  } catch (e) {
    log('could not report the result:', e.message);
    process.exit(1);
  }
  process.stdout.write(JSON.stringify(res) + '\n');
}

// uploadEvidence sends the video, the trace and the failed step's picture.
async function uploadEvidence(outDir) {
  const names = [];
  for (const n of readdirSync(outDir)) {
    if (n === 'trace.zip' || n === 'failed-step.png') names.push([n, n]);
  }
  const videos = join(outDir, 'video');
  if (existsSync(videos)) {
    let i = 0;
    for (const n of readdirSync(videos).filter((v) => v.endsWith('.webm'))) names.push([join('video', n), `replay-${++i}.webm`]);
  }
  for (const [from, to] of names) {
    const path = join(outDir, from);
    if (!statSync(path).size) continue;
    const type = to.endsWith('.png') ? 'image/png' : to.endsWith('.zip') ? 'application/zip' : 'video/webm';
    await put(opts['upload-url'] + to, readFileSync(path), type).catch((e) => log(`could not upload ${to}:`, e.message));
  }
}

// child runs the journey module in this process and writes its result.
async function child() {
  const { createRequire } = await import('node:module');
  const lib = await import('./bp-journey-lib.mjs');
  const root = process.env.BP_PW_ROOT || '/usr/lib/node_modules/@playwright/mcp/';
  const require = createRequire(root);
  // playwright-core from the Playwright MCP install: its bundle carries the
  // humanise patch, so text goes in key by key at a person's pace.
  const { chromium, devices } = require('playwright-core');
  const { expect } = require('playwright/test');

  const seed = Number(opts.seed);
  const inputs = {};
  const started = Date.now();
  const result = { status: 'passed', seed, inputs };
  const finish = (r) => {
    r.durationMs = Date.now() - started;
    writeFileSync(opts.result, JSON.stringify(r));
  };

  const launch = {};
  if (opts.cdp) {
    // The same flags as AI runs' browser (pw-config.json): CDP on 9222 for
    // the live screen and the console recorder.
    try { launch.args = JSON.parse(readFileSync('/opt/bp/pw-config.json', 'utf8')).browser.launchOptions.args; } catch { /* plain launch */ }
  }
  const proxy = process.env.HTTPS_PROXY || process.env.https_proxy;
  if (proxy) launch.proxy = { server: proxy, bypass: process.env.NO_PROXY || process.env.no_proxy || undefined };

  let browser, ctx, page;
  try {
    const mod = await import(opts.file);
    if (typeof mod.default !== 'function') throw new lib.StepFailed(0, '', new Error('the journey must export a default async function'));
    browser = await chromium.launch(launch);
    const name = opts.device ? lib.devices[opts.device] : '';
    if (opts.device && !name) log('ignoring unknown --device value');
    ctx = await browser.newContext({
      ...(name ? devices[name] : {}),
      ...(opts.profile ? { storageState: opts.profile } : {}),
      recordVideo: { dir: join(opts.out, 'video') },
    });
    await ctx.tracing.start({ screenshots: true, snapshots: true });
    page = await ctx.newPage();
    page.setDefaultTimeout(15000);
    humanise(page);
    const bp = lib.makeBp({ onFail: () => page.screenshot({ path: join(opts.out, 'failed-step.png') }) });
    const data = lib.makeData(seed, inputs);
    const limit = Number(process.env.BP_REPLAY_TIMEOUT_MS) || 10 * 60 * 1000;
    let timer;
    await Promise.race([
      mod.default({ page, data, bp, expect }),
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`the journey ran over ${Math.round(limit / 60000)} minutes`)), limit); }),
    ]).finally(() => clearTimeout(timer));
  } catch (e) {
    result.status = 'failed';
    result.step = e instanceof lib.StepFailed ? e.step : 0;
    if (e instanceof lib.StepFailed && e.stepName) result.stepName = e.stepName;
    result.error = lib.cleanError(e.message || e);
  }
  try {
    if (ctx) {
      await ctx.tracing.stop({ path: join(opts.out, 'trace.zip') }).catch(() => {});
      if (opts['save-profile']) await ctx.storageState({ path: opts['save-profile'] }).catch(() => {});
      await ctx.close();
    }
    if (browser) await browser.close();
  } catch (e) {
    log('closing the browser:', e.message);
  }
  finish(result);
  process.exit(0);
}

// humanise makes locator.fill type like a person, through the patched
// bundle's __bpHumanType, as AI runs' browser_type does.
function humanise(page) {
  const proto = Object.getPrototypeOf(page.locator('body'));
  const fill = proto.fill;
  if (proto.__bpHumanised || typeof globalThis.__bpHumanType !== 'function') return;
  proto.__bpHumanised = true;
  proto.fill = function (value, options) {
    // __bpHumanType clears with fill('') and falls back to fill for fields
    // with no keys to press (dates, ranges): those calls take the original.
    if (value === '' || typeof value !== 'string' || this.__bpTyping) return fill.call(this, value, options);
    this.__bpTyping = true;
    return globalThis.__bpHumanType(this, value, options).finally(() => { this.__bpTyping = false; });
  };
}
