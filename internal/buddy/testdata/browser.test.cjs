const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const context = vm.createContext({ window: {}, document: { querySelectorAll: () => [] } });
vm.runInContext(fs.readFileSync(`${__dirname}/../web/picker.js`, 'utf8'), context);
const search = (spools, query, archived = false, assigned = '0', selected = '0') => {
 context.spools = spools; context.query = query; context.archived = archived; context.assigned = assigned; context.selected = selected;
 return Array.from(vm.runInContext('searchSpools(spools, query, archived, assigned, selected)', context), s => s.ID);
};
const spools = [
 { ID: 7, Label: '#7 · Prusament Galaxy Purple PLA' },
 { ID: 12, Label: '#12 · Polymaker Purple PETG' },
 { ID: 17, Label: '#17 · Old Purple PLA', Archived: true },
];
test('spool search matches all terms regardless of case or order', () => {
 assert.deepEqual(search(spools, 'PLA PURPLE'), [7]);
 assert.deepEqual(search(spools, 'purple polymaker'), [12]);
 assert.deepEqual(search(spools, 'missing'), []);
});
test('archived spools are hidden unless requested or retained as a choice', () => {
 assert.deepEqual(search(spools, 'purple', true), [12,17,7]);
 assert.ok(search(spools, 'purple', false, '17').includes(17));
});
test('exact spool identifiers rank first', () => { assert.equal(search(spools, '7', true)[0], 7); });
