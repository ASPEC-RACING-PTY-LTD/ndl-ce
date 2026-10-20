import { useEffect, useState } from "react";
import { getInstanceTemplate, type InstanceTemplate } from "./api/client";

/** useTemplateFromURL loads the settings template named by ?template= on a create page. */
export function useTemplateFromURL(kind: string): { template: InstanceTemplate | null; error: string | null } {
  const [template, setTemplate] = useState<InstanceTemplate | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const id = new URLSearchParams(window.location.search).get("template");
    if (!id) {
      return;
    }
    let live = true;
    getInstanceTemplate(id)
      .then((t) => {
        if (!live) {
          return;
        }
        if (t.kind !== kind) {
          setError("That template is for a different instance type.");
          return;
        }
        setTemplate(t);
      })
      .catch((err: unknown) => live && setError(err instanceof Error ? err.message : "Could not read the template"));
    return () => {
      live = false;
    };
  }, [kind]);
  return { template, error };
}

/** num reads a number field from a template spec. */
export function num(spec: Record<string, unknown> | undefined, key: string): number | undefined {
  const v = spec?.[key];
  return typeof v === "number" && Number.isFinite(v) && v > 0 ? v : undefined;
}

/** str reads a string field from a template spec. */
export function str(spec: Record<string, unknown> | undefined, key: string): string | undefined {
  const v = spec?.[key];
  return typeof v === "string" && v ? v : undefined;
}

/** strMap reads a string-to-string map (environment) from a template spec. */
export function strMap(spec: Record<string, unknown> | undefined, key: string): Record<string, string> {
  const v = spec?.[key];
  const out: Record<string, string> = {};
  if (v && typeof v === "object" && !Array.isArray(v)) {
    for (const [k, x] of Object.entries(v)) {
      if (typeof x === "string") {
        out[k] = x;
      }
    }
  }
  return out;
}

/** createHref is where "Use template" goes for each kind. */
export function createHref(t: InstanceTemplate): string {
  const id = encodeURIComponent(t.id);
  switch (t.kind) {
    case "oci":
      return `/workloads/new/oci?template=${id}`;
    case "game-server": {
      const game = str(t.spec, "game_template_id");
      return `/game-servers/create?template=${id}${game ? `&game=${encodeURIComponent(game)}` : ""}`;
    }
    default:
      return `/workloads/new/system-container?template=${id}`;
  }
}
