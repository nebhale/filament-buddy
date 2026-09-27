'use strict';
const browserDateFormat = new Intl.DateTimeFormat(undefined, {
 year: 'numeric', month: 'short', day: 'numeric',
 hour: 'numeric', minute: '2-digit', second: '2-digit', timeZoneName: 'short',
});
function formatTimestamp(value) {
 if (!value || value.startsWith('0001-')) return null;
 const date = new Date(value);
 return Number.isFinite(date.getTime()) ? browserDateFormat.format(date) : null;
}
for (const time of document.querySelectorAll('time[datetime]')) {
 const text = formatTimestamp(time.dateTime);
 if (text) time.textContent = text;
}
function renderTimestamp(target, value) {
 const text = formatTimestamp(value);
 if (!text) { target.textContent = 'No markers yet'; return; }
 const time = document.createElement('time');
 time.dateTime = value;
 time.textContent = text;
 target.replaceChildren(time);
}
let dirty = false;
document.addEventListener('input', () => { dirty = true; });
for (const input of document.querySelectorAll('.spool-search')) {
 input.addEventListener('input', () => {
  const query = input.value.toLocaleLowerCase();
  for (const option of input.form.querySelector('select').options) {
   option.hidden = option.value !== '0' && !option.selected && !option.textContent.toLocaleLowerCase().includes(query);
  }
 });
}
for (const form of document.querySelectorAll('form[data-confirm]')) form.addEventListener('submit', e => {
 if (!window.confirm(form.dataset.confirm)) e.preventDefault();
});
for (const button of document.querySelectorAll('.copy')) button.addEventListener('click', async () => {
 const code = button.parentElement.querySelector('code');
 try { await navigator.clipboard.writeText(code.textContent); button.textContent = 'G-code copied'; }
 catch { const range = document.createRange(); range.selectNodeContents(code); const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range); button.textContent = 'Select the G-code and copy it with your browser'; }
});
const session = document.getElementById('session-state');
const printerCards = Array.from(document.querySelectorAll('[data-printer]'));
function renderPrinterStatus(card, p) {
 const state = card.querySelector('[data-role="state"]');
 state.textContent = p.Active ? 'Active' : p.Suppressed ? 'Closed manually' : 'Waiting';
 state.classList.toggle('active', Boolean(p.Active));
 const name = card.querySelector('[data-role="name"]');
 if (p.Active) {
  const link = document.createElement('a');
  link.href = `/sessions/${p.Active.ID}`;
  link.textContent = p.Active.Name;
  name.replaceChildren(link);
 } else name.textContent = 'No active session';
 card.querySelector('[data-role="count"]').textContent = p.Active ? `Section ${p.Active.Current} · ${(p.RecordedMG / 1000).toFixed(3)} g recorded` : '';
 card.querySelector('[data-role="error"]').textContent = p.LastError || '';
 card.querySelector('[data-role="suppression"]').textContent = p.Suppressed ? 'CHANGE markers are ignored until a new START marker or application restart.' : '';
 renderTimestamp(card.querySelector('[data-role="received"]'), p.LastEvent);
}
setInterval(async () => {
 if (document.hidden) return;
 if (session) {
  try {
   const response = await fetch(`/api/sessions/${session.dataset.id}`);
   if (!response.ok) return;
   const data = await response.json();
   if (String(data.Revision) !== session.dataset.revision) {
    if (dirty || document.querySelector('details[open]') || /INPUT|SELECT|BUTTON/.test(document.activeElement.tagName)) document.getElementById('updates').hidden = false;
    else window.location.reload();
   }
  } catch { /* A network outage must not discard an in-progress edit. */ }
  return;
 }
 if (!printerCards.length) return;
 try {
  const response = await fetch('/api/status');
  if (!response.ok) throw new Error('Service unreachable');
  const status = await response.json();
  for (const p of status) {
   const card = printerCards.find(card => card.dataset.printer === p.Printer.ID);
   if (!card) continue;
   renderPrinterStatus(card, p);
   // Keep the library and filter intact while the printer card updates live.
   if (card.dataset.sessionId !== (p.Active?.ID || '') || card.dataset.sessionRevision !== (p.Active ? String(p.Active.Revision) : '')) document.getElementById('updates').hidden = false;
  }
 } catch {
  for (const card of printerCards) card.querySelector('[data-role="error"]').textContent = 'Connection lost. Retrying…';
 }
}, 3000);
