export class Terminal {
  element: HTMLDivElement;
  cols = 80;
  rows = 24;
  private resizeHandler: ((size: { cols: number; rows: number }) => void) | null = null;

  constructor() {
    this.element = document.createElement("div");
    this.element.dataset.testid = "xterm";
    this.element.className = "xterm";
  }

  loadAddon(addon?: { activate?: (term: Terminal) => void }) {
    addon?.activate?.(this);
  }

  open(el: HTMLElement) {
    el.appendChild(this.element);
  }

  resize(cols: number, rows: number) {
    const nextCols = Math.max(1, Math.floor(cols));
    const nextRows = Math.max(1, Math.floor(rows));
    if (nextCols === this.cols && nextRows === this.rows) {
      return;
    }
    this.cols = nextCols;
    this.rows = nextRows;
    this.resizeHandler?.({ cols: this.cols, rows: this.rows });
  }

  onResize(handler: (size: { cols: number; rows: number }) => void) {
    this.resizeHandler = handler;
    return { dispose() {} };
  }

  write() {}

  dispose() {}

  onData() {}

  attachCustomKeyEventHandler() {
    return true;
  }
}
