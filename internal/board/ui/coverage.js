// Coverage: which persona has run which story. Rows are the personas, by
// role, with the personas a lead made under that lead; columns are the
// stories. A cell counts the runs and takes the colour of the latest one, so
// a gap (a story the persona's role has but nobody ran) stands out.
import { html, parseTime, fmtWhen } from './lib.js';

export const LEAD_STORY = '(lead)';

// isLead: a persona with any lead power ticked.
export const isLead = (p) => !!(p.lead && (p.lead.seeRuns || p.lead.createPersonas || p.lead.startRuns || p.lead.tidyBugs));

// coverageRows lays out the grid: one row per persona, in project order, a
// lead's personas right after it, then personas only old runs mention.
export function coverageRows(project, runs) {
  const roles = (project && project.roles) || [];
  const rows = [];
  const placed = new Set();
  const add = (row) => { if (!placed.has(row.name)) { placed.add(row.name); rows.push(row); } };
  const childrenOf = (lead) => roles.filter((r) => r.madeBy === lead);
  const stories = (r) => (r.stories || []).map((s) => s.name);
  for (const r of roles.filter((x) => !x.madeBy)) {
    for (const p of r.personas || []) {
      const lead = isLead(p);
      add({ name: p.name, role: r.name, stories: stories(r).concat(lead ? [LEAD_STORY] : []) });
      if (!lead) continue;
      for (const c of childrenOf(p.name)) {
        for (const cp of c.personas || []) add({ name: cp.name, role: c.name, madeBy: p.name, stories: stories(c) });
      }
    }
  }
  for (const c of roles.filter((x) => x.madeBy)) { // lead since removed
    for (const cp of c.personas || []) add({ name: cp.name, role: c.name, madeBy: c.madeBy, stories: stories(c) });
  }
  for (const r of runs) add({ name: r.persona, role: r.role || '', gone: true, stories: [] });

  const cols = [];
  const seen = new Set();
  const col = (s) => { if (!seen.has(s)) { seen.add(s); cols.push(s); } };
  for (const row of rows) row.stories.filter((s) => s !== LEAD_STORY).forEach(col);
  for (const r of runs) if (r.story !== LEAD_STORY) col(r.story);
  if (rows.some((row) => row.stories.includes(LEAD_STORY)) || runs.some((r) => r.story === LEAD_STORY)) col(LEAD_STORY);
  return { rows, cols };
}

const TONE = new Map([['done', 'ok'], ['failed', 'bad'], ['stuck', 'wait'], ['waiting', 'hold'], ['running', 'run'], ['pending', 'run']]);

export function Coverage({ project, runs, onPick }) {
  const { rows, cols } = coverageRows(project, runs);
  const cell = new Map(); // persona \n story -> runs, newest first
  for (const r of runs) {
    const k = `${r.persona}\n${r.story}`;
    if (!cell.has(k)) cell.set(k, []);
    cell.get(k).push(r);
  }
  for (const list of cell.values()) list.sort((a, b) => (parseTime(b.started) || 0) - (parseTime(a.started) || 0));
  let pairs = 0, gaps = 0;
  for (const row of rows) for (const s of row.stories) { pairs++; if (!cell.has(`${row.name}\n${s}`)) gaps++; }

  return html`
    <div class="coverage">
      <p class="small muted">${gaps ? `${gaps} of ${pairs} persona and story pairs never ran.` : `Every persona has run each of its stories.`}${' '}Each cell takes the colour of its latest run. Choose one to see its runs.</p>
      <div class="cov-wrap card flush">
        <table class="cov">
          <thead><tr><th class="cov-who">Persona</th>${cols.map((s) => html`<th key=${s} scope="col"><span>${s === LEAD_STORY ? 'Lead runs' : s}</span></th>`)}</tr></thead>
          <tbody>
            ${rows.map((row) => html`<tr key=${row.name} class=${row.gone ? 'gone' : ''}>
              <th scope="row" class=${'cov-who' + (row.madeBy ? ' child' : '')}>
                <b>${row.name}</b>
                <span class="small muted">${row.madeBy ? `made by ${row.madeBy}` : row.gone ? 'no longer in the project' : row.role}</span>
              </th>
              ${cols.map((s) => {
                const list = cell.get(`${row.name}\n${s}`);
                const applies = row.stories.includes(s);
                if (!list) return html`<td key=${s} class=${applies ? 'gap' : 'na'} title=${applies ? 'Never ran' : ''}>${applies ? html`<span>Not run</span>` : null}</td>`;
                const last = list[0];
                const bugs = list.reduce((n, r) => n + (r.bugs || 0), 0);
                return html`<td key=${s}>
                  <button type="button" class=${'cov-cell ' + (TONE.get(last.state) || 'idle')}
                      title=${`Latest ${last.state} ${fmtWhen(last.started)}`} onClick=${() => onPick(row.name, s)}>
                    <i aria-hidden="true"></i><span>${list.length === 1 ? '1 run' : `${list.length} runs`}</span>
                    ${bugs ? html`<em>${bugs === 1 ? '1 bug' : `${bugs} bugs`}</em>` : null}
                  </button></td>`;
              })}
            </tr>`)}
          </tbody>
        </table>
      </div>
    </div>`;
}
