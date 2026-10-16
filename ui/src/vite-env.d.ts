/// <reference types="vite/client" />

declare module "node:fs" {
  export function readFileSync(path: string, encoding: "utf8"): string;
}

declare module "node:path" {
  export function dirname(path: string): string;
  export function join(...paths: string[]): string;
}

declare module "node:url" {
  export function fileURLToPath(url: string | URL): string;
}

/** Build ID of this web UI, stamped by vite.config.ts. */
declare const __NDL_BUILD__: string;
