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
for (const button of document.querySelectorAll('.copy')) button.addEventListener('click', async () => {
 const code = button.parentElement.querySelector('code');
 try { await navigator.clipboard.writeText(code.textContent); button.textContent = 'G-code copied'; }
 catch { const range = document.createRange(); range.selectNodeContents(code); const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range); button.textContent = 'Select the G-code and copy it with your browser'; }
});
