// Tests that the project editor's draft keeps what it doesn't edit: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fromProject, toJSON } from '../ui/project.js';

test('a save keeps the logins and each persona\'s login', () => {
  const p = {
    name: 'p', sites: ['https://x.test'],
    logins: [{ kind: 'admin', username: 'ann@x.test', password: 'pw' }],
    roles: [{ name: 'owner', personas: [{ name: 'Tom', traits: [], identity: {}, login: 'admin' }, { name: 'Ann', traits: [], identity: {} }], stories: [] }],
  };
  const out = toJSON(fromProject(p));
  assert.deepEqual(out.logins, p.logins);
  assert.equal(out.roles[0].personas[0].login, 'admin');
  assert.equal('login' in out.roles[0].personas[1], false);
});

test('a project without logins saves without the key', () => {
  const out = toJSON(fromProject({ name: 'p', sites: ['https://x.test'], roles: [] }));
  assert.equal('logins' in out, false);
});
