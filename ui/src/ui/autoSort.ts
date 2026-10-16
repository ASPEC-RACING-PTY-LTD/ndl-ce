// autoSort makes every plain table sortable: click a header to sort by that
// column, click again to reverse, a third time to restore the original order.
// Tables that sort themselves (a header already has aria-sort) or opt out
// with data-nosort are left alone. Rows are reordered in the DOM and the
// order is reapplied whenever React re-renders the table body.

type SortState = { col: number; dir: 1 | -1; observer: MutationObserver; applying: boolean };

const states = new WeakMap<HTMLTableElement, SortState>();
const original = new WeakMap<HTMLTableRowElement, number>();
let seq = 0;

const units: Record<string, number> = {
  b: 1, kb: 1e3, mb: 1e6, gb: 1e9, tb: 1e12, pb: 1e15,
  kib: 1024, mib: 1024 ** 2, gib: 1024 ** 3, tib: 1024 ** 4, pib: 1024 ** 5,
};

function sortKey(cell: Element | undefined): { n: number | null; s: string } {
  if (!cell) {
    return { n: null, s: "" };
  }
  const raw = (cell.getAttribute("data-sort-value") ?? (cell as HTMLElement).innerText ?? cell.textContent ?? "").trim();
  const bytes = /^(-?[\d.,]+)\s*([kmgtp]?i?b)\b/i.exec(raw);
  if (bytes && units[bytes[2].toLowerCase()]) {
    return { n: parseFloat(bytes[1].replace(/,/g, "")) * units[bytes[2].toLowerCase()], s: raw };
  }
  const num = /^-?[\d,]*\.?\d+\s*%?$/.exec(raw);
  if (num) {
    return { n: parseFloat(raw.replace(/[,%\s]/g, "")), s: raw };
  }
  if (/^\d{4}-\d{2}-\d{2}/.test(raw)) {
    const t = Date.parse(raw);
    if (!Number.isNaN(t)) {
      return { n: t, s: raw };
    }
  }
  return { n: null, s: raw.toLowerCase() };
}

function compare(a: { n: number | null; s: string }, b: { n: number | null; s: string }): number {
  // Empty values always sort last.
  if (!a.s && b.s) return 1;
  if (a.s && !b.s) return -1;
  if (a.n != null && b.n != null) return a.n - b.n;
  return a.s.localeCompare(b.s, undefined, { numeric: true, sensitivity: "base" });
}

// groups keeps a full-width detail row (one cell spanning the table) with
// the row above it.
function groups(body: HTMLTableSectionElement): HTMLTableRowElement[][] {
  const out: HTMLTableRowElement[][] = [];
  for (const row of Array.from(body.rows)) {
    if (!original.has(row)) {
      original.set(row, seq++);
    }
    const detail = row.cells.length === 1 && row.cells[0].colSpan > 1;
    if (detail && out.length) {
      out[out.length - 1].push(row);
    } else {
      out.push([row]);
    }
  }
  return out;
}

function apply(table: HTMLTableElement) {
  const st = states.get(table);
  if (!st) {
    return;
  }
  st.applying = true;
  for (const body of Array.from(table.tBodies)) {
    const gs = groups(body);
    if (gs.length < 2 || gs.some((g) => g[0].cells.length === 1 && g[0].cells[0].colSpan > 1)) {
      continue;
    }
    const sorted = [...gs].sort((a, b) => {
      const c = compare(sortKey(a[0].cells[st.col]), sortKey(b[0].cells[st.col]));
      return c !== 0 ? c * st.dir : (original.get(a[0]) ?? 0) - (original.get(b[0]) ?? 0);
    });
    const flat = sorted.flat();
    const current = Array.from(body.rows);
    if (flat.every((r, i) => current[i] === r)) {
      continue;
    }
    for (const r of flat) {
      body.appendChild(r);
    }
  }
  st.observer.takeRecords();
  st.applying = false;
}

function restore(table: HTMLTableElement) {
  for (const body of Array.from(table.tBodies)) {
    const rows = Array.from(body.rows).sort((a, b) => (original.get(a) ?? 0) - (original.get(b) ?? 0));
    for (const r of rows) {
      body.appendChild(r);
    }
  }
}

function markHeaders(table: HTMLTableElement, col: number, dir: number) {
  const head = table.tHead?.rows[table.tHead.rows.length - 1];
  if (!head) return;
  Array.from(head.cells).forEach((th, i) => {
    th.setAttribute("data-autosort", i === col && dir ? (dir > 0 ? "asc" : "desc") : "none");
  });
}

function sortable(th: HTMLTableCellElement, table: HTMLTableElement): boolean {
  if (table.hasAttribute("data-nosort") || th.hasAttribute("data-nosort")) return false;
  if (table.tHead?.querySelector("th[aria-sort]")) return false;
  if (th.querySelector("input, select, button, a")) return false;
  return Boolean(th.textContent?.trim());
}

function onClick(ev: MouseEvent) {
  const th = (ev.target as Element | null)?.closest?.("thead th") as HTMLTableCellElement | null;
  const table = th?.closest("table") as HTMLTableElement | null;
  if (!th || !table || !sortable(th, table)) {
    return;
  }
  const col = th.cellIndex;
  let st = states.get(table);
  if (!st) {
    const observer = new MutationObserver(() => {
      const s = states.get(table);
      if (s && !s.applying && s.dir) {
        requestAnimationFrame(() => apply(table));
      }
    });
    st = { col, dir: 1, observer, applying: false };
    states.set(table, st);
    for (const body of Array.from(table.tBodies)) {
      groups(body);
      observer.observe(body, { childList: true, subtree: true, characterData: true });
    }
  } else if (st.col !== col) {
    st.col = col;
    st.dir = 1;
  } else if (st.dir === 1) {
    st.dir = -1;
  } else {
    st.observer.disconnect();
    states.delete(table);
    restore(table);
    markHeaders(table, -1, 0);
    return;
  }
  markHeaders(table, st.col, st.dir);
  apply(table);
}

let installed = false;

/** installTableSorting turns on header-click sorting for plain tables. */
export function installTableSorting() {
  if (installed || typeof document === "undefined") {
    return;
  }
  installed = true;
  document.addEventListener("click", onClick);
}
