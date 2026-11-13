// Generated UK test data for journeys. No dependencies, so a seed draws the
// same values on every image. Normal values come from the seed's own stream;
// edge cases (awkward but valid input) come from a second stream, so a
// journey that excludes one never moves any other value its seed draws.

import { rng } from './bp-journey-lib.mjs';

const EDGE_SALT = 0x2545f491;
const EDGE_RATE = 0.3;

// edgeReplay tells whether a seed's replay carries edge cases: the first
// number of its edge stream.
export function edgeReplay(seed) {
  return rng(seed ^ EDGE_SALT)() < EDGE_RATE;
}

export function edgeStream(seed) {
  return rng(seed ^ EDGE_SALT);
}

const FIRST = ['Amira', 'Tom', 'Megan', 'Oliver', 'Priya', 'James', 'Chloe', 'Harry', 'Aisha', 'George', 'Sophie', 'Jack', 'Emily', 'Mohammed', 'Grace', 'Daniel', 'Lucy', 'Ravi', 'Hannah', 'Callum', 'Fatima', 'Liam', 'Ella', 'Ryan', 'Zara', 'Owen', 'Isla', 'Kwame', 'Ruby', 'Ethan', 'Niamh', 'Arjun', 'Freya', 'Lewis', 'Maya', 'Connor', 'Holly', 'Samuel', 'Leah', 'Yusuf', 'Erin', 'Ben'];
const LAST = ['Patel', 'Hale', 'Smith', 'Jones', 'Taylor', 'Brown', 'Williams', 'Wilson', 'Khan', 'Evans', 'Thomas', 'Roberts', 'Walker', 'Wright', 'Robinson', 'Thompson', 'White', 'Hughes', 'Edwards', 'Green', 'Hall', 'Wood', 'Harris', 'Clarke', 'Jackson', 'Turner', 'Hill', 'Moore', 'Cooper', 'Ward', 'Morris', 'King', 'Baker', 'Shah', 'Begum', 'Kerr', 'Mill', 'Murray', 'Campbell', 'Ali', 'Okafor', 'Price'];
const STREETS = ['Mill Lane', 'Station Road', 'High Street', 'Church Lane', 'Park Avenue', 'Victoria Road', 'Green Lane', 'Manor Road', 'Kings Road', 'Queens Road', 'New Street', 'Grange Road', 'School Lane', 'The Crescent', 'Albert Road', 'York Road', 'Springfield Road', 'North Street', 'West Street', 'London Road', 'Chapel Street', 'Mill Road', 'Elm Grove', 'Highfield Road', 'Windsor Road', 'Brook Street', 'Market Street', 'Bridge Street', 'Castle Street', 'Water Lane', 'Orchard Way', 'The Avenue', 'Cedar Close', 'Hawthorn Drive', 'Meadow View', 'Ash Grove', 'Beech Road', 'Oak Lane', 'Forge Lane', 'Riverside Walk', 'St Johns Road', 'Fairfield Road'];
// Town and postcode pairs that belong together.
const PLACES = [['Leeds', 'LS1 4AP'], ['Manchester', 'M1 1AE'], ['Birmingham', 'B1 1BB'], ['Bristol', 'BS1 4DJ'], ['Sheffield', 'S1 2HE'], ['Liverpool', 'L1 8JQ'], ['Newcastle upon Tyne', 'NE1 7RU'], ['Nottingham', 'NG1 5FS'], ['Leicester', 'LE1 5WW'], ['Cardiff', 'CF10 1EP'], ['Edinburgh', 'EH1 1YZ'], ['Glasgow', 'G1 1XQ'], ['Belfast', 'BT1 5GS'], ['Southampton', 'SO14 7DU'], ['Brighton', 'BN1 1EE'], ['Plymouth', 'PL1 2AA'], ['Norwich', 'NR2 1NH'], ['York', 'YO1 7HH'], ['Oxford', 'OX1 1DP'], ['Cambridge', 'CB2 3QJ'], ['Exeter', 'EX1 1JG'], ['Derby', 'DE1 2FS'], ['Hull', 'HU1 2AA'], ['Bath', 'BA1 1SU'], ['Swansea', 'SA1 3SN'], ['Aberdeen', 'AB10 1YP'], ['Dundee', 'DD1 3BA'], ['Preston', 'PR1 2HE'], ['Coventry', 'CV1 5RR'], ['Reading', 'RG1 1JX'], ['Milton Keynes', 'MK9 3EP'], ['Bradford', 'BD1 1HY'], ['Stoke-on-Trent', 'ST1 1HP'], ['Wolverhampton', 'WV1 1RQ'], ['Sunderland', 'SR1 3AA'], ['Ipswich', 'IP1 1BQ'], ['Gloucester', 'GL1 2EH'], ['Lincoln', 'LN1 1XP'], ['Carlisle', 'CA3 8QG'], ['Chester', 'CH1 1EF'], ['Canterbury', 'CT1 2HX'], ['Inverness', 'IV1 1HT']];
const TRADES = ['Plumbing', 'Electrical', 'Joinery', 'Roofing', 'Decorating', 'Landscaping', 'Cleaning', 'Bookkeeping', 'Catering', 'Tiling', 'Plastering', 'Glazing', 'Locksmiths', 'Removals', 'Heating', 'Building', 'Carpentry', 'Gardening', 'Design', 'Printing', 'Photography', 'Tutoring', 'Consulting', 'Bakery', 'Florist', 'Upholstery', 'Brickwork', 'Flooring', 'Scaffolding', 'Drainage', 'Fencing', 'Signwriting', 'Courier', 'Hair', 'Beauty', 'Fitness', 'Joinery Works', 'Kitchens', 'Bathrooms', 'Windows', 'Surveying', 'Accounts'];
const ITEMS = ['Boiler service', 'Labour (2 hours)', 'Call-out fee', 'Copper pipe, 15mm', 'Radiator valve', 'Website design', 'Monthly bookkeeping', 'Garden clearance', 'Paint and materials', 'Consultation', 'Kitchen fitting', 'Replacement lock', 'Deep clean', 'Delivery', 'Wedding cake', 'Tiling, per square metre', 'Annual gas safety check', 'Fence panel', 'Logo design', 'Travel'];

const pad = (n, w) => String(n).padStart(w, '0');

// londonToday is today's date in Europe/London as YYYY-MM-DD.
function londonToday() {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/London' }).format(new Date());
}

function addDays(iso, days) {
  const d = new Date(iso + 'T00:00:00Z');
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

// nearestLeapDay is the 29 February nearest today.
function nearestLeapDay(today) {
  const y = Number(today.slice(0, 4));
  let best = '';
  let bestDiff = Infinity;
  for (let k = y - 4; k <= y + 4; k++) {
    if (!((k % 4 === 0 && k % 100 !== 0) || k % 400 === 0)) continue;
    const iso = `${k}-02-29`;
    const diff = Math.abs(Date.parse(iso) - Date.parse(today));
    if (diff < bestDiff) { best = iso; bestDiff = diff; }
  }
  return best;
}

// generators returns the data.* generators. nextNormal and nextEdge are the
// two streams; keep(name, v) records a value and returns it; edgeKeep(name,
// id) records the edge case the value just kept carries. fixedToday
// (YYYY-MM-DD) is the day dates are relative to, so a replay resumed after
// a wait draws the dates it drew before; empty is today in London.
export function generators(nextNormal, nextEdge, keep, edgeKeep, fixedToday = '') {
  const today = () => fixedToday || londonToday();
  const int = (min, max) => min + Math.floor(nextNormal() * (max - min + 1));
  const pick = (list) => list[Math.floor(nextNormal() * list.length)];

  // The edge stream's first use decides whether this replay carries edge
  // cases and how many (one or two).
  let budget = -1;
  const edge = (o, ids) => {
    if (budget < 0) {
      const isEdge = nextEdge() < EDGE_RATE;
      const size = nextEdge() < 0.5 ? 1 : 2;
      budget = isEdge ? size : 0;
    }
    const a = nextEdge();
    const b = nextEdge();
    if (budget === 0 || a >= 0.5) return '';
    // Spent even when the call refuses every id, so excluding an edge case
    // never hands one to a later call.
    budget--;
    const exclude = (o && o.exclude) || [];
    const left = o && o.edges === false ? [] : ids.filter((id) => !exclude.includes(id));
    return left.length ? left[Math.floor(b * left.length)] : '';
  };
  const out = (name, v, ids) => {
    keep(name, v);
    for (const id of ids) if (id) edgeKeep(name, id);
    return v;
  };

  const personName = (o) => {
    const first = pick(FIRST);
    const last = pick(LAST);
    const id = edge(o, ['apostrophe', 'accents', 'leading-space']);
    const v = { apostrophe: `${first} O'Brien`, accents: 'Zoë Ågren', 'leading-space': `  ${first} ${last}` }[id] || `${first} ${last}`;
    return [v, id];
  };

  const businessName = (o) => {
    const size = (o && o.size) || 'small';
    if (!['sole', 'small', 'firm'].includes(size)) throw new Error("data.businessName size is 'sole', 'small' or 'firm'");
    const a = pick(LAST);
    const b = pick(LAST);
    const trade = pick(TRADES);
    const v = size === 'sole' ? `${a} ${trade}` : size === 'small' ? `${a} & ${b} Ltd` : `${a} ${b} ${trade} Group Holdings Limited`;
    const id = edge(o, ['long', 'leading-space', 'line-break']);
    if (id === 'long') {
      let s = `${v} and Associates Property Maintenance and Building Services of Greater Manchester and the North West`;
      while (s.length < 120) s += ' Limited';
      s = s.slice(0, 120);
      return [s.endsWith(' ') ? s.slice(0, 119) + '.' : s, id];
    }
    if (id === 'leading-space') return [`  ${v}`, id];
    if (id === 'line-break') {
      const i = v.lastIndexOf(' ');
      return [v.slice(0, i) + '\n' + v.slice(i + 1), id];
    }
    return [v, ''];
  };

  const address = (o) => {
    const line1 = `${int(1, 220)} ${pick(STREETS)}`;
    const [town, postcode] = pick(PLACES);
    const id = edge(o, ['line-break']);
    return [{ line1: id ? `Flat 2\n${line1}` : line1, town, postcode }, id];
  };

  const price = (o) => {
    const { min = 1, max = 500, round = false } = o || {};
    if (!(max >= min)) throw new Error('data.price needs min <= max');
    const v = round ? int(Math.ceil(min), Math.floor(max)) : Math.round((min + nextNormal() * (max - min)) * 100) / 100;
    const id = edge(o, ['tiny', 'huge']);
    return [{ tiny: 0.01, huge: 999999.99 }[id] ?? v, id];
  };

  const quantity = (o) => {
    const { min = 1, max = 10 } = o || {};
    if (!Number.isInteger(min) || !Number.isInteger(max) || max < min) throw new Error('data.quantity needs whole numbers with min <= max');
    const v = int(min, max);
    const id = edge(o, ['zero']);
    return [id ? 0 : v, id];
  };

  const date = (o) => {
    const { from = -30, to = 30 } = o || {};
    if (!Number.isInteger(from) || !Number.isInteger(to) || to < from) throw new Error('data.date needs whole days with from <= to');
    const day = today();
    const v = addDays(day, int(from, to));
    const id = edge(o, ['leap-day']);
    return [id ? nearestLeapDay(day) : v, id];
  };

  const dateRange = (o) => {
    const { from = -30, to = 0, minDays = 7, maxDays = 30 } = o || {};
    if (![from, to, minDays, maxDays].every(Number.isInteger) || to < from || maxDays < minDays || minDays < 0) throw new Error('data.dateRange needs whole days with from <= to and minDays <= maxDays');
    const end = addDays(today(), int(from, to));
    const start = addDays(end, -int(minDays, maxDays));
    const id = edge(o, ['reversed']);
    return [id ? { start: end, end: start } : { start, end }, id];
  };

  const lineItems = (o) => {
    const { min = 1, max = 4, round = false, maxPrice = 500 } = o || {};
    if (!Number.isInteger(min) || !Number.isInteger(max) || min < 1 || max < min) throw new Error('data.lineItems needs whole numbers with 1 <= min <= max');
    const n = int(min, max);
    const items = [];
    const ids = [];
    for (let i = 0; i < n; i++) {
      let description = pick(ITEMS);
      const did = edge(o, ['line-break', 'leading-space']);
      if (did === 'line-break') description = `${description}\nas quoted`;
      if (did === 'leading-space') description = `  ${description}`;
      const [q, qid] = quantity({ exclude: o && o.exclude, edges: o && o.edges });
      const [p, pid] = price({ max: maxPrice, round, exclude: o && o.exclude, edges: o && o.edges });
      items.push({ description, quantity: q, price: p });
      ids.push(did, qid, pid);
    }
    return [items, ...ids];
  };

  const wrap = (name, fn) => (o = {}) => {
    const [v, ...ids] = fn(o);
    return out(name, v, ids);
  };
  return {
    personName: wrap('personName', personName),
    businessName: wrap('businessName', businessName),
    address: wrap('address', address),
    postcode: () => keep('postcode', pick(PLACES)[1]),
    phone: () => keep('phone', `07700 900${pad(int(0, 999), 3)}`),
    // vatNumber is GB plus nine digits passing HMRC's mod-97 check.
    vatNumber: () => {
      const d = Array.from({ length: 7 }, () => int(0, 9));
      const sum = d.reduce((s, x, i) => s + x * (8 - i), 0);
      return keep('vatNumber', `GB${d.join('')}${pad((97 - (sum % 97)) % 97, 2)}`);
    },
    companyNumber: () => keep('companyNumber', pad(int(0, 99999999), 8)),
    price: wrap('price', price),
    quantity: wrap('quantity', quantity),
    date: wrap('date', date),
    dateRange: wrap('dateRange', dateRange),
    lineItems: wrap('lineItems', lineItems),
  };
}
