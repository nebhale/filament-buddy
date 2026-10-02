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
for (const select of document.querySelectorAll('.filter select')) {
 select.addEventListener('change', () => select.form.requestSubmit());
}
