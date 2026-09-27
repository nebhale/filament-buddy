const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(`${__dirname}/../web/app.js`, 'utf8');
function environment({ dirty = false, status = 200 } = {}) {
 let callback, reloads = 0;
 const notice = { hidden: true };
 const session = { dataset: { id: 'test', revision: '1' } };
 const doc = {
  hidden: false, activeElement: { tagName: 'BODY' },
  querySelectorAll: () => [], querySelector: () => null,
  getElementById: id => id === 'session-state' ? session : notice,
  addEventListener: (type, fn) => { if (dirty) fn(); }
 };
 vm.runInNewContext(source, {
  document: doc, window: { location: { reload() { reloads++; } } },
  setInterval(fn) { callback = fn; },
  fetch: async () => ({ ok: status === 200, json: async () => ({ Revision: 2 }) })
 });
 return { tick: () => callback(), notice, doc, reloads: () => reloads };
}
test('refreshes updated sections when there are no edits', async () => {
 const e = environment(); await e.tick(); assert.equal(e.reloads(), 1);
});
test('preserves edits and offers a reload instead', async () => {
 const e = environment({ dirty: true }); await e.tick(); assert.equal(e.reloads(), 0); assert.equal(e.notice.hidden, false);
});
test('an expired authenticated page does not reload repeatedly', async () => {
 const e = environment({ status: 401 }); await e.tick(); assert.equal(e.reloads(), 0);
});
test('hidden tabs do not refresh', async () => {
 const e = environment(); e.doc.hidden = true; await e.tick(); assert.equal(e.reloads(), 0);
});
