import type { HealthResponse } from "./generated/openapi";

const health: HealthResponse = { status: "ok", service: "ndl-control" };

export function App() {
  return (
    <main>
      <h1>No-dal Community Edition</h1>
      <p>CI works</p>
      <p>
        Service {health.service} reports {health.status}. This page is a
        toolchain skeleton. It does not show infrastructure data.
      </p>
    </main>
  );
}
