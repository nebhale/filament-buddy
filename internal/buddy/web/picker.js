"use strict";

function searchSpools(spools, query, includeArchived, assigned, selected) {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const exact = query.trim().replace(/^#/, "");
  const rank = spool => String(spool.ID) === exact ? 0 : spool.Label.toLocaleLowerCase().startsWith(query.trim().toLocaleLowerCase()) ? 1 : 2;
  return spools.filter(spool => (!spool.Archived || includeArchived || String(spool.ID) === assigned || String(spool.ID) === selected) &&
    terms.every(term => `${spool.ID} ${spool.Label}`.toLocaleLowerCase().includes(term)))
    .sort((a, b) => rank(a) - rank(b) || a.Label.localeCompare(b.Label) || a.ID - b.ID);
}

class SpoolPicker {
  constructor(form, owner) {
    this.form = form; this.owner = owner; this.select = form.querySelector('select[name="spool"]');
    this.query = ""; this.highlight = null;
    this.select.hidden = true; this.select.dataset.preserve = "picker-select";
    form.dataset.pickerReady = "true";
    form.querySelector(".native-spool-label")?.setAttribute("hidden", "");
    const root = this.root = document.createElement("div"); root.className = "spool-picker"; root.dataset.clientOwned = "";
    const id = `picker-${form.querySelector('[name="section"]').value}`;
    const label = document.createElement("label"); label.htmlFor = id; label.textContent = "Find a spool";
    const input = this.input = document.createElement("input");
    input.id = id; input.type = "search"; input.autocomplete = "off"; input.placeholder = "Search name, material, or spool ID";
    input.setAttribute("role", "combobox"); input.setAttribute("aria-autocomplete", "list"); input.setAttribute("aria-expanded", "false"); input.setAttribute("aria-controls", `${id}-results`);
    const selected = this.selected = document.createElement("p"); selected.className = "picker-selection";
    const popup = this.popup = document.createElement("div"); popup.className = "picker-popup"; popup.hidden = true;
    const tools = document.createElement("div"); tools.className = "picker-tools";
    const archivedLabel = document.createElement("label");
    const archived = this.archived = document.createElement("input"); archived.type = "checkbox";
    archivedLabel.append(archived, document.createTextNode(" Include archived"));
    const refresh = document.createElement("button"); refresh.type = "button"; refresh.className = "button secondary"; refresh.textContent = "Refresh catalog";
    tools.append(archivedLabel, refresh);
    const results = this.results = document.createElement("div"); results.id = `${id}-results`; results.setAttribute("role", "listbox"); results.setAttribute("aria-label", "Matching spools");
    const count = this.count = document.createElement("p"); count.className = "picker-count"; count.setAttribute("role", "status"); count.setAttribute("aria-live", "polite");
    const freshness = this.freshness = document.createElement("p"); freshness.className = "picker-freshness";
    popup.append(tools, results, count); root.append(label, input, selected, freshness, popup);
    form.insertBefore(root, form.querySelector(".form-row"));
    input.addEventListener("focus", () => this.open());
    input.addEventListener("input", () => { this.query = input.value; this.highlight = null; this.open(); });
    archived.addEventListener("change", () => this.render());
    input.addEventListener("keydown", event => {
      if (["ArrowDown", "ArrowUp"].includes(event.key)) {
        event.preventDefault(); this.open();
        const ids = this.visible.map(s => String(s.ID));
        const index = ids.indexOf(this.highlight);
        this.highlight = ids[Math.max(0, Math.min(ids.length - 1, index + (event.key === "ArrowDown" ? 1 : -1)))];
        this.render(); document.getElementById(`${id}-option-${this.highlight}`)?.scrollIntoView({ block: "nearest" });
      } else if (event.key === "Enter") { event.preventDefault(); if (!popup.hidden && this.highlight !== null) this.choose(this.highlight); }
      else if (event.key === "Escape") { event.preventDefault(); this.close(); }
    });
    root.addEventListener("focusout", () => setTimeout(() => { if (!root.contains(document.activeElement)) this.close(); }, 0));
    refresh.addEventListener("click", async () => {
      refresh.disabled = true; this.freshness.textContent = "Refreshing catalog…";
      try {
        const response = await fetch("/spools/refresh", { method: "POST", headers: { Accept: "application/json" }, body: new URLSearchParams({ csrf: form.querySelector('[name="csrf"]').value }), signal: AbortSignal.timeout(20000) });
        if (!response.ok) throw new Error("Catalog refresh failed. Cached spools are still available.");
        await window.buddyUpdates?.refresh();
      } catch (error) { this.freshness.textContent = error.message; }
      finally { refresh.disabled = false; }
    });
    this.sync();
  }
  open() { this.popup.hidden = false; this.input.setAttribute("aria-expanded", "true"); this.render(); }
  close() { this.popup.hidden = true; this.input.setAttribute("aria-expanded", "false"); this.input.removeAttribute("aria-activedescendant"); }
  choose(id) {
    this.ensureOption(id); this.select.value = id;
    this.input.value = this.owner.label(id); this.query = "";
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
    if (!this.query && this.input.value) this.input.value = this.owner.label(this.select.value);
    this.selected.textContent = `Selected: ${this.owner.label(this.select.value)}`;
    this.freshness.textContent = this.owner.refreshing ? "Refreshing catalog… Cached spools remain available." : this.owner.error ? `Catalog unavailable: ${this.owner.error}. Using cached spools.` : this.owner.refreshed ? `Catalog updated ${formatTimestamp(this.owner.refreshed)}` : "Using cached spool catalog";
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
      const swatch = document.createElementNS("http://www.w3.org/2000/svg", "svg"); swatch.setAttribute("width", "20"); swatch.setAttribute("height", "20"); swatch.setAttribute("aria-hidden", "true");
      const circle = document.createElementNS("http://www.w3.org/2000/svg", "circle"); circle.setAttribute("cx", "10"); circle.setAttribute("cy", "10"); circle.setAttribute("r", "8"); circle.setAttribute("fill", /^[0-9a-f]{6}$/i.test(spool.Color || "") ? `#${spool.Color}` : "#9a8daf"); swatch.append(circle);
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
  spools: [], error: "", refreshed: null, refreshing: false,
  label(id) { return String(id) === "0" ? "No spool" : this.spools.find(s => String(s.ID) === String(id))?.Label || `Spool #${id} (unavailable)`; },
  enhance() {
    for (const form of document.querySelectorAll(".spool-form")) {
      if (!form.buddyPicker) {
        // Before the shared catalog arrives, retain labels from the native fallback.
        if (!this.spools.length) this.spools = Array.from(form.querySelector("select").options).filter(o => o.value !== "0").map(o => ({ ID: Number(o.value), Label: o.textContent.replace(/ \(archived\)$/, ""), Archived: o.textContent.endsWith(" (archived)") }));
        form.buddyPicker = new SpoolPicker(form, this);
      }
    }
  },
  sync(form) { form.buddyPicker?.sync(); },
  catalog(data) {
    this.spools = data.spools || []; this.error = data.error; this.refreshing = data.refreshing; this.refreshed = data.refreshed_at?.startsWith("0001-") ? null : data.refreshed_at;
    this.enhance();
    for (const form of document.querySelectorAll(".spool-form")) this.sync(form);
  },
};
window.BuddyPicker.enhance();
