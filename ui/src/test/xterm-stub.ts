export class Terminal {
  element: HTMLDivElement;

  constructor() {
    this.element = document.createElement("div");
  }

  loadAddon() {}

  open(el: HTMLElement) {
    el.appendChild(this.element);
  }

  write() {}

  dispose() {}

  onData() {}

  onResize() {}

  attachCustomKeyEventHandler() {
    return true;
  }
}
