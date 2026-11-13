#!/usr/bin/env node
// bp-replay runs a recorded journey with Playwright and no model.
//
//   bp-replay --journey <file> --self [--seed N] [--expect-fail-step N] [SETTINGS]
//     An AI run proving its journey: prints one JSON result line; exits 0
//     only when the result proves the revision (passed, stopped at its first
//     bp.waitFor, or failed exactly at --expect-fail-step). With no SETTINGS
//     it uses the ones bp-browser recorded for the run's persona, so the
//     journey renders as the scheduled replay will.
//
//   bp-replay --claim-url URL [--seed N] [SETTINGS]
//     A replay run: spends its one-time claim for the run's URLs, fetches
//     the code (and, after a wait, where to resume), restores the persona's
//     browser, runs, uploads the evidence, saves the browser back and PUTs
//     the result. A bp.waitFor past the resume point ends it with status
//     waiting.
//
// SETTINGS are the persona's device and accessibility settings, stable keys
// from the director: --device KEY --assistive KEY --vision KEY --display KEY
// (bp-a11y.mjs turns them into browser settings).
//
// The run's URLs carry its token, and everything in the pod, the journey
// too, can read every process's command line and environment. So the pod's
// command holds only the claim, spent before the journey starts; the URLs
// live in this process's memory and reach the live screen and the console
// recorder on their stdin. The journey runs in a child process given only
// paths. Logs go to stderr (never a URL); stdout carries only the result line.
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomInt } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { edgeReplay } from './bp-data.mjs';
import { contextOptions, visionType, readSettings } from './bp-a11y.mjs';

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
const LOOK = ['device', 'assistive', 'vision', 'display'];
// look is the persona's settings given on the command line.
const look = Object.fromEntries(LOOK.filter((k) => opts[k]).map((k) => [k, opts[k]]));
if (opts.child) {
  await child();
} else if (opts.self) {
  await selfMode();
} else if (opts['claim-url']) {
  await runMode();
} else {
  log('usage: bp-replay --journey FILE --self | --claim-url URL');
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
function runChild({ file, seed, settings = {}, outDir, profile, saveProfile, cdp, resume, today }) {
  const args = [self, '--child', '--file', file, '--seed', String(seed), '--out', outDir];
  if (resume) args.push('--resume', resume);
  if (today) args.push('--today', today);
  for (const k of LOOK) if (settings[k]) args.push('--' + k, settings[k]);
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
  // The AI proves the happy path: with no --seed, pick one that carries no
  // edge cases. Scheduled replays and --seed N keep what their seed decides.
  let seed = seedOf(opts.seed);
  for (let i = 0; opts.seed === undefined && i < 100 && edgeReplay(seed); i++) seed = seedOf(undefined);
  const outDir = mkdtempSync(join(tmpdir(), 'bp-self-'));
  const settings = Object.keys(look).length ? look : readSettings(process.env.BP_SETTINGS || '/tmp/bp-pw/settings.json');
  const res = await runChild({ file: opts.journey, seed, settings, outDir });
  process.stdout.write(JSON.stringify(res) + '\n');
  const want = opts['expect-fail-step'] ? Number(opts['expect-fail-step']) : 0;
  const proven = want ? res.status === 'failed' && res.step === want : res.status === 'passed' || res.status === 'waiting';
  process.exit(proven ? 0 : 1);
}

async function put(url, body, type) {
  const r = await fetch(url, { method: 'PUT', body, headers: { 'Content-Type': type } });
  if (!r.ok) throw new Error(`HTTP ${r.status}`);
}

// claim spends the run's one-time claim and returns its URLs. With no URLs
// there is nowhere to report, so a refused claim ends bp-replay.
async function claim() {
  const u = opts['claim-url'];
  let r;
  try {
    r = await fetch(u, { method: 'POST' });
  } catch (e) {
    log('could not claim this replay:', e.message);
    process.exit(1);
  }
  if (!r.ok) { log(`could not claim this replay (HTTP ${r.status})`); process.exit(1); }
  const paths = await r.json();
  const urls = {};
  for (const k of ['journey', 'report', 'upload', 'profile', 'live', 'console']) {
    if (typeof paths[k] !== 'string') { log(`the claim gave no ${k} URL`); process.exit(1); }
    urls[k] = new URL(paths[k], u).href;
  }
  return urls;
}

// helper starts the live screen or the console recorder with its URL on
// stdin, never on its command line.
function helper(script, url) {
  const p = spawn(process.execPath, [script, '--url-stdin'], { stdio: ['pipe', 'ignore', 'ignore'], detached: true });
  p.stdin.end(url + '\n');
  p.unref();
}

async function runMode() {
  const seed = seedOf(opts.seed);
  const urls = await claim();
  const outDir = mkdtempSync(join(tmpdir(), 'bp-replay-'));
  let res;
  try {
    const r = await fetch(urls.journey);
    if (!r.ok) throw new Error(`could not fetch the journey (HTTP ${r.status})`);
    const fetched = await r.json();
    if (typeof fetched.code !== 'string') throw new Error('the journey fetched has no code');
    const file = join(outDir, 'journey.mjs');
    writeFileSync(file, fetched.code);
    let resume = '';
    if (fetched.resume) {
      resume = join(outDir, 'resume.json');
      writeFileSync(resume, JSON.stringify(fetched.resume));
    }
    let profile = '';
    try {
      const pr = await fetch(urls.profile);
      if (pr.status === 200) {
        profile = join(outDir, 'profile.json');
        writeFileSync(profile, Buffer.from(await pr.arrayBuffer()));
      } else if (pr.status !== 204) log(`no profile restored (HTTP ${pr.status})`);
    } catch { log('no profile restored'); }
    // The live screen and the console recorder attach over CDP, as in AI runs.
    helper('/opt/bp/bp-screen.mjs', urls.live);
    helper('/opt/bp/bp-console.mjs', urls.console);
    const saveProfile = join(outDir, 'profile-out.json');
    // The day the replay's chain began: a resumed replay draws the dates it drew before.
    const today = /^\d{4}-\d{2}-\d{2}$/.test(fetched.today || '') ? fetched.today : '';
    res = await runChild({ file, seed, settings: look, outDir, profile, saveProfile, cdp: true, resume, today });
    await uploadEvidence(outDir, urls.upload);
    if (existsSync(saveProfile)) {
      await put(urls.profile, readFileSync(saveProfile), 'application/json').catch((e) => log('profile not saved:', e.message));
    }
  } catch (e) {
    res = { status: 'failed', step: 0, error: String(e.message || e).slice(0, 2000), seed };
  }
  try {
    await put(urls.report, JSON.stringify(res), 'application/json');
  } catch (e) {
    log('could not report the result:', e.message);
    process.exit(1);
  }
  process.stdout.write(JSON.stringify(res) + '\n');
}

// uploadEvidence sends the video, the trace and the failed step's picture.
async function uploadEvidence(outDir, uploadUrl) {
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
    await put(uploadUrl + to, readFileSync(path), type).catch((e) => log(`could not upload ${to}:`, e.message));
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
    ctx = await browser.newContext({
      ...contextOptions(look, devices),
      ...(opts.profile ? { storageState: opts.profile } : {}),
      recordVideo: { dir: join(opts.out, 'video') },
    });
    // Every tab, popups too, as the persona sees it.
    const vision = visionType(look.vision);
    const see = (pg) => ctx.newCDPSession(pg).then((s) => s.send('Emulation.setEmulatedVisionDeficiency', { type: vision }))
      .catch((e) => log('could not apply the eyesight setting:', e.message));
    if (vision) ctx.on('page', see);
    await ctx.tracing.start({ screenshots: true, snapshots: true });
    page = await ctx.newPage();
    if (vision) await see(page); // before the journey's first step
    page.setDefaultTimeout(15000);
    humanise(page);
    const resume = opts.resume ? JSON.parse(readFileSync(opts.resume, 'utf8')) : undefined;
    const bp = lib.makeBp({ onFail: () => page.screenshot({ path: join(opts.out, 'failed-step.png') }), resume });
    const data = lib.makeData(seed, inputs, { today: opts.today || '' });
    const limit = Number(process.env.BP_REPLAY_TIMEOUT_MS) || 10 * 60 * 1000;
    let timer;
    await Promise.race([
      mod.default({ page, data, bp, expect }),
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`the journey ran over ${Math.round(limit / 60000)} minutes`)), limit); }),
    ]).finally(() => clearTimeout(timer));
  } catch (e) {
    if (e instanceof lib.WaitReached) {
      // The journey waits for an email or a time: the director parks the
      // replay and resumes it after this wait.
      result.status = 'waiting';
      result.step = e.step;
      result.wait = { n: e.n, until: e.until };
    } else {
      result.status = 'failed';
      result.step = e instanceof lib.StepFailed ? e.step : 0;
      if (e instanceof lib.StepFailed && e.stepName) result.stepName = e.stepName;
      result.error = lib.cleanError(e.message || e);
    }
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
