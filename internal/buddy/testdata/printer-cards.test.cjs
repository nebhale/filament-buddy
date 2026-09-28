const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const context = vm.createContext({ document: { querySelectorAll: () => [] } });
vm.runInContext(fs.readFileSync(`${__dirname}/../web/app.js`, 'utf8'), context);
test('missing marker timestamps remain distinguishable from real dates', () => {
 for (const value of ['', '0001-01-01T00:00:00Z', 'invalid']) {
  context.value = value;
  assert.equal(vm.runInContext('formatTimestamp(value)', context), null);
 }
 assert.equal(typeof vm.runInContext('formatTimestamp("2026-09-26T21:00:00Z")', context), 'string');
});
