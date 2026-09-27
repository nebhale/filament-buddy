const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(`${__dirname}/../web/app.js`, 'utf8');
function element() {
 return { textContent: '', classList: { toggle() {} }, replaceChildren(child) { this.child = child; this.textContent = child.textContent; } };
}
function browser() {
 let tick, reloads = 0, requestCount = 0, status = [], fail = false;
 const notice = { hidden: true };
 const cards = ['c1', 'mini'].map(id => {
  const fields = Object.fromEntries(['state','name','count','received','error','suppression'].map(key => [key,element()]));
  return { dataset: { printer:id, sessionId:'', sessionRevision:'' }, fields,
   querySelector(selector) { return fields[selector.match(/data-role="([^"]+)"/)[1]]; } };
 });
 const doc = {
  hidden:false, activeElement:{tagName:'SELECT'},
  querySelectorAll(selector) { return selector === '[data-printer]' ? cards : []; },
  querySelector() { return null; },
  getElementById(id) { return id === 'updates' ? notice : null; },
  addEventListener() {}, createElement:element,
 };
 vm.runInNewContext(source, {
  document:doc, window:{ location:{reload(){ reloads++; }} },
  setInterval(fn){ tick=fn; },
  fetch:async () => { requestCount++; if (fail) throw new Error('offline'); return {ok:true,json:async()=>status}; },
 });
 return { cards, notice, doc, tick:()=>tick(), setStatus(value){status=value;}, offline(value){fail=value;},
  reloads:()=>reloads, requests:()=>requestCount };
}
function printer(id, extra={}) {
 return { Printer:{ID:id}, Active:null, RecordedMG:0, LastEvent:'0001-01-01T00:00:00Z', LastError:'', Suppressed:false, ...extra };
}
test('printer cards follow independent sessions without reloading or losing the filter', async () => {
 const b=browser();
 b.setStatus([printer('mini'),printer('c1')]); await b.tick();
 assert.equal(b.cards[0].fields.state.textContent,'Waiting');
 assert.equal(b.cards[0].fields.received.textContent,'No markers yet');
 assert.equal(b.notice.hidden,true);
 b.setStatus([printer('mini'),printer('c1',{Active:{ID:'print-1',Revision:2,Name:'<A & B>',Current:3},RecordedMG:12345,LastEvent:'2026-09-26T21:00:00Z'})]); await b.tick();
 const card=b.cards[0];
 assert.equal(card.fields.name.child.href,'/sessions/print-1');
 assert.equal(card.fields.name.textContent,'<A & B>');
 assert.equal(card.fields.count.textContent,'Section 3 · 12.345 g recorded');
 assert.equal(card.fields.received.child.dateTime,'2026-09-26T21:00:00Z');
 assert.equal(b.cards[1].fields.name.textContent,'No active session');
 assert.equal(b.notice.hidden,false);
 assert.equal(b.doc.activeElement.tagName,'SELECT');
 assert.equal(b.reloads(),0);
 b.setStatus([printer('c1',{Suppressed:true}),printer('mini')]); await b.tick();
 assert.equal(card.fields.state.textContent,'Closed manually');
 assert.equal(card.fields.name.textContent,'No active session');
 assert.equal(card.fields.count.textContent,'');
 assert.match(card.fields.suppression.textContent,/CHANGE markers are ignored/);
 b.setStatus([printer('c1',{Active:{ID:'print-2',Revision:1,Name:'Next print',Current:1}})]); await b.tick();
 assert.equal(card.fields.state.textContent,'Active');
 assert.equal(card.fields.suppression.textContent,'');
});
test('connection failures are visible and recover on the next successful poll', async () => {
 const b=browser(); b.offline(true); await b.tick();
 for(const card of b.cards) assert.equal(card.fields.error.textContent,'Connection lost. Retrying…');
 b.offline(false); b.setStatus([printer('c1',{LastError:'Conflicting marker'}),printer('mini')]); await b.tick();
 assert.equal(b.cards[0].fields.error.textContent,'Conflicting marker');
 assert.equal(b.cards[1].fields.error.textContent,'');
 assert.equal(b.reloads(),0);
});
test('printer polling pauses while the page is hidden', async () => {
 const b=browser(); b.doc.hidden=true; await b.tick(); assert.equal(b.requests(),0);
 b.doc.hidden=false; await b.tick(); assert.equal(b.requests(),1);
});
