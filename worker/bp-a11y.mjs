// bp-a11y turns a persona's device and accessibility settings (stable keys
// from the director's profile catalogue) into browser settings, for AI runs
// (bp-pw-setup, bp-gate) and replays (bp-replay) alike. No dependencies.
import { readFileSync } from 'node:fs';

// Phones and tablets: Playwright device descriptors.
export const PHONES = {
  'iphone-15': 'iPhone 15',
  'iphone-15-pro-max': 'iPhone 15 Pro Max',
  'pixel-7': 'Pixel 7',
  'galaxy-s24': 'Galaxy S24',
  ipad: 'iPad (gen 11)',
  'ipad-mini': 'iPad Mini',
};

// Desktops: a viewport. Unset or unknown devices get the first.
const DESKTOPS = {
  desktop: { width: 1280, height: 800 },
  'desktop-large': { width: 1920, height: 1080 },
  'laptop-small': { width: 1366, height: 768 },
};

// Context options a descriptor carries that a browser context takes.
const CONTEXT_KEYS = ['viewport', 'screen', 'userAgent', 'deviceScaleFactor', 'isMobile', 'hasTouch'];

const DISPLAY = {
  'reduced-motion': { reducedMotion: 'reduce' },
  'high-contrast': { forcedColors: 'active', contrast: 'more' },
  dark: { colorScheme: 'dark' },
};

const VISION = {
  blurred: 'blurredVision',
  'low-contrast': 'reducedContrast',
  deuteranopia: 'deuteranopia',
  protanopia: 'protanopia',
  tritanopia: 'tritanopia',
  achromatopsia: 'achromatopsia',
};

const half = (s) => s && { width: Math.round(s.width / 2), height: Math.round(s.height / 2) };

// contextOptions is the browser context for settings {device, assistive,
// display, locale, timezone}; devices is Playwright's descriptor table. Zoom
// lays the page out as 200% browser zoom does: half the CSS viewport at twice
// the pixel density. locale (a BCP 47 tag) and timezone (an IANA zone) are
// the project's: how pages format dates and numbers, and the clock they read.
export function contextOptions({ device = '', assistive = '', display = '', locale = '', timezone = '' } = {}, devices) {
  const d = PHONES[device] && devices[PHONES[device]];
  const out = {};
  if (d) {
    for (const k of CONTEXT_KEYS) if (d[k] !== undefined) out[k] = d[k];
  } else {
    out.viewport = { ...(DESKTOPS[device] || DESKTOPS.desktop) };
  }
  if (assistive === 'zoom') {
    out.viewport = half(out.viewport);
    if (out.screen) out.screen = half(out.screen);
    out.deviceScaleFactor = (out.deviceScaleFactor || 1) * 2;
  }
  if (locale) out.locale = locale;
  if (timezone) out.timezoneId = timezone;
  return { ...out, ...(DISPLAY[display] || {}) };
}

// localeEnv is the environment that starts Chrome in a language: Chrome draws
// date and time fields in its own language, not the page's, and on Linux
// takes it from these. {} for no locale, or one that is no plain BCP 47 tag.
export function localeEnv(locale = '') {
  if (!/^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/.test(locale)) return {};
  const posix = locale.replace(/-/g, '_');
  const lang = posix.split('_')[0];
  return { LANG: posix + '.UTF-8', LANGUAGE: posix === lang ? lang : `${posix}:${lang}`, LC_ALL: posix + '.UTF-8' };
}

// visionType is the Chrome vision deficiency for an eyesight key, or ''.
export const visionType = (key) => VISION[key] || '';

// GLOBAL_BLOCK is refused to every persona: the browser's settings are the
// persona's, and the agent may not undo them.
export const GLOBAL_BLOCK = ['browser_resize', 'browser_emulate_media'];

// Everything that points at an element or runs script, which a keyboard
// user cannot do: clicking through JavaScript is still clicking.
const POINTER = ['browser_click', 'browser_hover', 'browser_drag', 'browser_drop', 'browser_select_option',
  'browser_fill_form', 'browser_type', 'browser_evaluate', 'browser_run_code_unsafe'];

// toolRules is what the tool gate does for an assistive setting: tools it
// hides and refuses (with why), tools it adds, and Playwright MCP's image
// responses ('' leaves the default).
export function toolRules(assistive = '') {
  switch (assistive) {
    case 'keyboard-only':
      return { block: [...GLOBAL_BLOCK, ...POINTER], add: ['browser_type_focused'], imageResponses: '',
        why: 'this persona uses the keyboard only: move with browser_press_key (Tab, Shift+Tab, arrow keys, Enter, Space) and type into the focused field with browser_type_focused' };
    case 'screen-reader':
      return { block: [...GLOBAL_BLOCK], add: [], imageResponses: 'omit', why: 'the browser is set to the persona\'s device and settings' };
  }
  return { block: [...GLOBAL_BLOCK], add: [], imageResponses: '', why: 'the browser is set to the persona\'s device and settings' };
}

// SETTINGS are the keys of a run's browser settings.
export const SETTINGS = ['device', 'assistive', 'vision', 'display', 'locale', 'timezone'];

// readSettings reads the settings bp-pw-setup recorded for the run, or {}.
export function readSettings(path) {
  try {
    const s = JSON.parse(readFileSync(path, 'utf8'));
    const out = {};
    for (const k of SETTINGS) if (typeof s[k] === 'string' && s[k]) out[k] = s[k];
    return out;
  } catch {
    return {};
  }
}
