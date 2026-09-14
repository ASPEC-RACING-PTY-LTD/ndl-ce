import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

function fromConfig(rel: string): string {
  return new URL(rel, import.meta.url).pathname;
}

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    alias: [
      { find: /^monaco-editor$/, replacement: fromConfig("./src/test/monaco-stub.ts") },
      { find: /^@monaco-editor\/react$/, replacement: fromConfig("./src/test/monaco-react-stub.tsx") },
      { find: /^@xterm\/xterm$/, replacement: fromConfig("./src/test/xterm-stub.ts") },
      { find: /^@xterm\/addon-fit$/, replacement: fromConfig("./src/test/xterm-fit-stub.ts") },
      { find: "@xterm/xterm/css/xterm.css", replacement: fromConfig("./src/test/empty.css") },
    ],
  },
});
