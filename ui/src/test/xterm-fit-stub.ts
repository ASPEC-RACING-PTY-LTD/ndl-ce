type FitTerm = {
  element?: HTMLElement;
  resize: (cols: number, rows: number) => void;
};

export class FitAddon {
  terminal: FitTerm | null = null;

  activate(term: FitTerm) {
    this.terminal = term;
  }

  fit() {
    const parent = this.terminal?.element?.parentElement;
    if (!this.terminal || !parent) {
      return;
    }
    const width = parent.clientWidth;
    const height = parent.clientHeight;
    if (width < 2 || height < 2) {
      return;
    }
    const cols = Math.max(2, Math.floor(width / 9));
    const rows = Math.max(1, Math.floor(height / 17));
    this.terminal.resize(cols, rows);
  }

  proposeDimensions() {
    return { cols: 80, rows: 24 };
  }

  dispose() {}
}
