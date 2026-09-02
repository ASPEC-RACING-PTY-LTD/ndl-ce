export class Terminal {
  element: HTMLDivElement;

  constructor() {
    this.element = document.createElement("div");
    this.element.dataset.testid = "xterm";
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
