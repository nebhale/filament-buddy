"use strict";

function searchSpools(spools, query, includeArchived, assigned, selected) {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const exact = query.trim().replace(/^#/, "");
  const rank = spool => String(spool.ID) === exact ? 0 : spool.Label.toLocaleLowerCase().startsWith(query.trim().toLocaleLowerCase()) ? 1 : 2;
  return spools.filter(spool => (!spool.Archived || includeArchived || String(spool.ID) === assigned || String(spool.ID) === selected) &&
    terms.every(term => `${spool.ID} ${spool.Label}`.toLocaleLowerCase().includes(term)))
    .sort((a, b) => rank(a) - rank(b) || a.Label.localeCompare(b.Label) || a.ID - b.ID);
}

function spoolSwatch(color) {
  const swatch = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  swatch.setAttribute("width", "20"); swatch.setAttribute("height", "20"); swatch.setAttribute("aria-hidden", "true");
  const circle = document.createElementNS("http://www.w3.org/2000/svg", "circle");
  circle.setAttribute("cx", "10"); circle.setAttribute("cy", "10"); circle.setAttribute("r", "8");
  circle.setAttribute("fill", spoolColor(color)); swatch.append(circle);
  return swatch;
}
function spoolColor(color) { return /^[0-9a-f]{6}$/i.test(color || "") ? `#${color}` : "#9a8daf"; }

class SpoolPicker {
  constructor(form, owner) {
    this.form = form; this.owner = owner; this.select = form.querySelector('select[name="spool"]');
    this.query = ""; this.highlight = null; this.editing = false; this.searching = false;
    this.select.hidden = true; this.select.dataset.preserve = "picker-select";
    form.dataset.pickerReady = "true";
    form.querySelector(".native-spool-label")?.setAttribute("hidden", "");
    const root = this.root = document.createElement("div"); root.className = "spool-picker"; root.dataset.clientOwned = "";
    const display = this.display = document.createElement("div"); display.className = "spool-assignment";
    const swatch = this.swatch = spoolSwatch();
    const name = this.name = document.createElement("p"); name.className = "spool-label";
    const edit = this.edit = document.createElement("button"); edit.type = "button"; edit.className = "spool-edit";
    const pencil = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    for (const [key, value] of Object.entries({ viewBox: "0 0 24 24", width: "18", height: "18", fill: "none", stroke: "currentColor", "stroke-width": "1.8", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" })) pencil.setAttribute(key, value);
    for (const d of ["m16 3 5 5-12 12-6 1 1-6Z", "m14 5 5 5"]) { const path = document.createElementNS("http://www.w3.org/2000/svg", "path"); path.setAttribute("d", d); pencil.append(path); }
    const assign = document.createElement("span"); assign.textContent = "+ Assign spool";
    edit.append(pencil, assign); display.append(swatch, name, edit);
    const editor = this.editor = document.createElement("div"); editor.className = "spool-editor"; editor.hidden = true;
    const id = `picker-${form.querySelector('[name="section"]').value}`;
    const label = document.createElement("label"); label.htmlFor = id; label.className = "sr-only"; label.textContent = this.select.getAttribute("aria-label");
    const input = this.input = document.createElement("input");
    input.id = id; input.type = "search"; input.autocomplete = "off";
    input.setAttribute("role", "combobox"); input.setAttribute("aria-autocomplete", "list"); input.setAttribute("aria-expanded", "false"); input.setAttribute("aria-controls", `${id}-results`);
    const popup = this.popup = document.createElement("div"); popup.className = "picker-popup"; popup.hidden = true;
    const tools = document.createElement("div"); tools.className = "picker-tools";
    const archivedLabel = document.createElement("label");
    const archived = this.archived = document.createElement("input"); archived.type = "checkbox";
    archivedLabel.append(archived, document.createTextNode(" Include archived"));
    const refresh = document.createElement("button"); refresh.type = "button"; refresh.className = "button secondary"; refresh.textContent = "Refresh catalog";
    tools.append(archivedLabel, refresh);
    const results = this.results = document.createElement("div"); results.id = `${id}-results`; results.setAttribute("role", "listbox"); results.setAttribute("aria-label", "Matching spools");
    const count = this.count = document.createElement("p"); count.className = "picker-count"; count.setAttribute("role", "status"); count.setAttribute("aria-live", "polite");
    const feedback = this.feedback = document.createElement("p"); feedback.className = "picker-feedback"; feedback.setAttribute("role", "status");
    popup.append(tools, results, count); editor.append(label, input, popup, feedback); root.append(display, editor);
    form.insertBefore(root, form.querySelector(".form-row"));
    this.save = form.querySelector('button[type="submit"]');
    const cancel = this.cancelButton = document.createElement("button"); cancel.type = "button"; cancel.className = "button secondary"; cancel.textContent = "Cancel"; cancel.dataset.clientOwned = "";
    form.querySelector(".form-row").append(cancel);
    // Keep focus in the editor when the shared save handler disables buttons.
    form.addEventListener("submit", () => {
      if (form.contains(document.activeElement)) { input.focus({ preventScroll: true }); this.close(); }
    });
    edit.addEventListener("click", () => this.begin());
    cancel.addEventListener("click", () => this.cancel());
    form.addEventListener("keydown", event => {
      if (event.key === "Escape" && !event.isComposing) { event.preventDefault(); this.cancel(); }
    });
    input.addEventListener("focus", () => this.open());
    input.addEventListener("input", () => { this.query = input.value; this.searching = input.value.length > 0; this.highlight = null; this.syncButtons(); this.open(); });
    archived.addEventListener("change", () => this.render());
    input.addEventListener("keydown", event => {
      if (event.isComposing) return;
      if (["ArrowDown", "ArrowUp"].includes(event.key)) {
        event.preventDefault(); this.open();
        const ids = this.visible.map(s => String(s.ID));
        const index = ids.indexOf(this.highlight);
        this.highlight = ids[Math.max(0, Math.min(ids.length - 1, index + (event.key === "ArrowDown" ? 1 : -1)))];
        this.render(); document.getElementById(`${id}-option-${this.highlight}`)?.scrollIntoView({ block: "nearest" });
      } else if (event.key === "Enter") {
        event.preventDefault();
        if (!popup.hidden && this.highlight !== null) this.choose(this.highlight);
        else if (popup.hidden && !this.save.disabled) form.requestSubmit(this.save);
      }
    });
    root.addEventListener("focusout", () => setTimeout(() => { if (!root.contains(document.activeElement)) this.close(); }, 0));
    refresh.addEventListener("click", async () => {
      refresh.disabled = true; this.feedback.textContent = "Refreshing catalog…";
      try {
        const response = await fetch("/spools/refresh", { method: "POST", headers: { Accept: "application/json" }, body: new URLSearchParams({ csrf: form.querySelector('[name="csrf"]').value }), signal: AbortSignal.timeout(20000) });
        if (!response.ok) throw new Error("Catalog refresh failed. Cached spools are still available.");
        await window.buddyUpdates?.refresh();
      } catch (error) { this.feedback.textContent = error.message; }
      finally { refresh.disabled = false; }
    });
    this.sync();
  }
  begin() {
    this.editing = true; this.root.dataset.editing = "";
    this.display.hidden = true; this.editor.hidden = false;
    this.query = ""; this.searching = false; this.highlight = null;
    this.input.value = "";
    this.syncButtons(); this.input.focus({ preventScroll: true }); this.input.select(); this.open();
  }
  finish() {
    const focused = this.form.contains(document.activeElement);
    this.editing = false; delete this.root.dataset.editing;
    this.display.hidden = false; this.editor.hidden = true;
    this.query = ""; this.searching = false; this.close();
    if (focused) this.edit.focus({ preventScroll: true });
  }
  cancel() {
    const state = buddyFormState(this.form);
    if (state.saving || state.unconfirmed || this.form.closest("[data-removed-draft]")) return;
    this.ensureOption(state.latest); this.select.value = state.latest;
    this.form.querySelector('[name="expected_spool"]').value = state.expected;
    state.base = state.latest; state.dirty = state.conflict = state.error = false;
    delete this.saved;
    this.form.querySelector(":scope > .form-message")?.remove();
    this.finish(); this.sync();
  }
  syncButtons() {
    const state = buddyFormState(this.form);
    const busy = state.saving || state.unconfirmed || Boolean(this.form.closest("[data-removed-draft]"));
    this.save.disabled = busy || this.searching;
    this.cancelButton.disabled = busy;
  }
  open() { this.popup.hidden = false; this.input.setAttribute("aria-expanded", "true"); this.render(); }
  close() { this.popup.hidden = true; this.input.setAttribute("aria-expanded", "false"); this.input.removeAttribute("aria-activedescendant"); }
  choose(id) {
    this.ensureOption(id); this.select.value = id;
    this.input.value = ""; this.query = ""; this.searching = false;
    this.select.dispatchEvent(new Event("input", { bubbles: true }));
    this.sync(); this.close(); this.input.focus({ preventScroll: true }); this.close();
  }
  ensureOption(id) {
    if (!Array.from(this.select.options).some(o => o.value === id)) {
      const option = document.createElement("option"); option.value = id; option.textContent = this.owner.label(id); this.select.append(option);
    }
  }
  sync() {
    const state = buddyFormState(this.form);
    this.ensureOption(state.latest);
    this.input.placeholder = this.owner.label(this.select.value);
    const unassigned = state.latest === "0";
    this.name.textContent = this.owner.label(state.latest);
    this.name.hidden = unassigned; this.swatch.toggleAttribute("hidden", unassigned);
    this.swatch.querySelector("circle").setAttribute("fill", spoolColor(this.owner.spools.find(s => String(s.ID) === state.latest)?.Color));
    this.edit.classList.toggle("unassigned", unassigned);
    this.edit.setAttribute("aria-label", this.select.getAttribute("aria-label").replace(/^Spool/, unassigned ? "Assign spool" : "Change spool"));
    this.edit.setAttribute("title", unassigned ? "Assign spool" : "Change spool");
    this.feedback.textContent = this.owner.refreshing ? "Refreshing catalog… Cached spools remain available." : this.owner.error ? `Catalog unavailable: ${this.owner.error}. Using cached spools.` : "";
    if (this.saved !== undefined && !state.saving && !state.unconfirmed && !state.error) {
      if (this.select.value !== this.saved || this.searching || state.conflict) delete this.saved;
      else if (state.latest === this.saved && state.expected === this.saved) { delete this.saved; this.finish(); }
    }
    this.syncButtons();
    if (!this.popup.hidden) this.render();
  }
  render() {
    const assigned = buddyFormState(this.form).latest;
    const found = searchSpools(this.owner.spools, this.query, this.archived.checked, assigned, this.select.value);
    const matches = found.length;
    const retained = new Set([assigned, this.select.value].filter(value => value !== "0"));
    const choices = found.slice(0, 49);
    // Reserve space for both saved and draft selections without displacing one
    // with the other, even in a large catalog. Keep exact matches ranked first.
    for (const value of retained) {
      if (!choices.some(s => String(s.ID) === value)) {
        if (choices.length >= 49) {
          const index = choices.findLastIndex(s => !retained.has(String(s.ID)));
          choices.splice(index, 1);
        }
        choices.push({ ...(this.owner.spools.find(s => String(s.ID) === value) || {}), ID: Number(value), Label: `${this.owner.label(value)} (${value === this.select.value ? "selected" : "assigned"})` });
      }
    }
    const visible = this.visible = [{ ID: 0, Label: "No spool" }, ...choices];
    if (!visible.some(s => String(s.ID) === this.highlight)) this.highlight = null;
    this.results.replaceChildren();
    for (const spool of visible) {
      const option = document.createElement("div"); const value = String(spool.ID);
      option.id = `${this.input.id}-option-${value}`; option.setAttribute("role", "option"); option.setAttribute("aria-selected", String(value === this.highlight)); option.dataset.value = value;
      option.className = "picker-option";
      const swatch = spoolSwatch(spool.Color);
      const text = document.createElement("span"); text.textContent = `${spool.Label}${spool.Archived ? " (archived)" : ""}${spool.Unavailable ? " (unavailable)" : ""}`;
      if (spool.RemainingMG != null) { const weight = document.createElement("small"); weight.textContent = `${(spool.RemainingMG / 1000).toFixed(1)} g remaining`; text.append(weight); }
      option.append(swatch, text);
      option.addEventListener("pointerdown", event => event.preventDefault());
      option.addEventListener("click", () => this.choose(value));
      this.results.append(option);
    }
    this.count.textContent = matches > 49 ? `${matches} matching spools · showing up to 49. Narrow your search for more.` : `${matches} matching spool${matches === 1 ? "" : "s"}`;
    if (this.highlight !== null) this.input.setAttribute("aria-activedescendant", `${this.input.id}-option-${this.highlight}`);
    else this.input.removeAttribute("aria-activedescendant");
  }
}
window.BuddyPicker = {
  spools: [], error: "", refreshing: false,
  label(id) { return String(id) === "0" ? "No spool" : this.spools.find(s => String(s.ID) === String(id))?.Label || `Spool #${id} (unavailable)`; },
  enhance() {
    for (const form of document.querySelectorAll(".spool-form")) {
      if (!form.buddyPicker) {
        // Before the shared catalog arrives, retain labels from the native fallback.
        if (!this.spools.length) this.spools = Array.from(form.querySelector("select").options).filter(o => o.value !== "0").map(o => ({ ID: Number(o.value), Label: o.textContent.replace(/ \(archived\)$/, ""), Archived: o.textContent.endsWith(" (archived)") }));
        form.buddyPicker = new SpoolPicker(form, this);
      }
      this.sync(form);
    }
  },
  sync(form) { form.buddyPicker?.sync(); },
  saved(form, value) { if (form.buddyPicker) form.buddyPicker.saved = value; },
  catalog(data) {
    this.spools = data.spools || []; this.error = data.error; this.refreshing = data.refreshing;
    this.enhance();
  },
};
window.BuddyPicker.enhance();
