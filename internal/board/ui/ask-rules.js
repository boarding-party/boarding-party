// Wording for a lead's ask to raise one of its limits (runs.OpenAsk).

const money = (n) => '$' + (Number(n) || 0).toFixed(2);

const LIMITS = {
  maxChildren: (n) => `${n} persona slot${n === 1 ? '' : 's'}`,
  dailyRuns: (n) => `${n} child run${n === 1 ? '' : 's'} a day`,
  dailyBudget: (n) => `${money(n)} a day`,
};

const value = (limit, n) => (limit === 'dailyBudget' ? money(n) : String(n));

// askLine is the ask in a sentence, or '' for a limit the board does not know.
export function askLine(a) {
  const say = LIMITS[a.limit];
  return say ? `Asks for ${say(a.to)} (now ${value(a.limit, a.now)}).` : '';
}

export const raiseLabel = (a) => `Raise to ${value(a.limit, a.to)}`;

export const keepLabel = (a) => `Keep at ${value(a.limit, a.now)}`;

// askSubject reads an ask's or a no's lead log subject ("maxChildren 3");
// anything else comes back as it is.
function askSubject(subject) {
  const [limit, to] = String(subject).split(' ');
  const say = LIMITS[limit];
  return say && to !== undefined && !Number.isNaN(Number(to)) ? say(Number(to)) : subject;
}

const VERBS = { ask: 'asked', decline: 'you kept' };

// logLine is a lead log entry's verb and subject as the decisions list
// shows them.
export function logLine(a) {
  return VERBS[a.action] ? [VERBS[a.action], askSubject(a.subject)] : [a.action, a.subject];
}
