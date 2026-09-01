import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

const specPath = "api/openapi/nodal.v1.yaml";
const outPath = "ui/src/generated/openapi.ts";
const spec = parseYaml(readFileSync(specPath, "utf8"));

if (!spec || spec.openapi == null || !spec.components?.schemas) {
  console.error("OpenAPI spec is missing openapi version or components.schemas");
  process.exit(1);
}

const schemas = spec.components.schemas;
const server = spec.servers?.[0]?.url ?? "";
const lines = [
  `/* Generated from ${specPath}. Do not edit by hand. */`,
  "",
];

for (const [name, schema] of Object.entries(schemas)) {
  lines.push(emitInterface(name, schema));
  lines.push("");
}

const paths = spec.paths ?? {};
for (const [pathKey, item] of Object.entries(paths)) {
  const op = item.get ?? item.post ?? item.put ?? item.delete;
  const id = op?.operationId ?? pathKey.replaceAll("/", "_");
  const typeName = toPascal(id) + "Path";
  const full = (server.replace(/\/$/, "") + pathKey).replace(/\/{2,}/g, "/");
  lines.push(`export type ${typeName} = ${JSON.stringify(full)};`);
  lines.push("");
}

const body = lines.join("\n").replace(/\n+$/, "\n");
mkdirSync(dirname(outPath), { recursive: true });
writeFileSync(outPath, body);

for (const name of Object.keys(schemas)) {
  if (!body.includes(`export interface ${name}`)) {
    console.error(`generator missed schema ${name}`);
    process.exit(1);
  }
}
if (Object.keys(paths).length === 0) {
  console.error("OpenAPI spec has no paths");
  process.exit(1);
}

console.log(`Wrote ${outPath}`);

function emitInterface(name, schema) {
  if (schema.type !== "object") {
    throw new Error(`schema ${name}: only object schemas are generated in Phase 0`);
  }
  const required = new Set(schema.required ?? []);
  const props = schema.properties ?? {};
  const fields = Object.entries(props).map(([field, prop]) => {
    const optional = required.has(field) ? "" : "?";
    return `  ${field}${optional}: ${tsType(prop)};`;
  });
  return `export interface ${name} {\n${fields.join("\n")}\n}`;
}

function tsType(prop) {
  if (prop.$ref) {
    return String(prop.$ref).split("/").pop();
  }
  if (prop.enum) {
    return prop.enum.map((v) => JSON.stringify(v)).join(" | ");
  }
  if (prop.type === "array") {
    const inner = tsType(prop.items ?? { type: "string" });
    const needsParens = inner.includes("|");
    return `${needsParens ? `(${inner})` : inner}[]`;
  }
  switch (prop.type) {
    case "string":
      return "string";
    case "integer":
    case "number":
      return "number";
    case "boolean":
      return "boolean";
    case "object":
      return "Record<string, unknown>";
    default:
      throw new Error(`unsupported OpenAPI type ${JSON.stringify(prop)}`);
  }
}

function toPascal(value) {
  return String(value)
    .split(/[^A-Za-z0-9]+/)
    .filter(Boolean)
    .map((part) => part[0].toUpperCase() + part.slice(1))
    .join("");
}

function parseYaml(text) {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  let i = 0;

  function indentOf(line) {
    return line.match(/^ */)?.[0].length ?? 0;
  }

  function peek() {
    while (i < lines.length && (lines[i].trim() === "" || lines[i].trimStart().startsWith("#"))) {
      i += 1;
    }
    return i < lines.length ? lines[i] : null;
  }

  function parseValue(minIndent) {
    const line = peek();
    if (line == null) return null;
    const indent = indentOf(line);
    if (indent < minIndent) return null;
    const trimmed = line.trim();
    if (trimmed.startsWith("- ")) {
      return parseSeq(indent);
    }
    if (trimmed.includes(":")) {
      return parseMap(indent);
    }
    i += 1;
    return scalar(trimmed);
  }

  function parseMap(indent) {
    const obj = {};
    while (true) {
      const line = peek();
      if (line == null) break;
      const cur = indentOf(line);
      if (cur < indent) break;
      if (cur > indent) {
        throw new Error(`unexpected nested indent: ${line}`);
      }
      const trimmed = line.trim();
      const colon = trimmed.indexOf(":");
      if (colon < 0) {
        throw new Error(`expected mapping: ${line}`);
      }
      const key = unquote(trimmed.slice(0, colon).trim());
      const rest = trimmed.slice(colon + 1).trim();
      i += 1;
      if (rest !== "") {
        obj[key] = scalar(rest);
        continue;
      }
      const next = peek();
      if (next == null || indentOf(next) <= indent) {
        obj[key] = null;
        continue;
      }
      obj[key] = parseValue(indent + 1);
    }
    return obj;
  }

  function parseSeq(indent) {
    const arr = [];
    while (true) {
      const line = peek();
      if (line == null) break;
      const cur = indentOf(line);
      if (cur < indent) break;
      const trimmed = line.trim();
      if (!trimmed.startsWith("- ")) break;
      const rest = trimmed.slice(2).trim();
      i += 1;
      if (rest === "") {
        arr.push(parseValue(indent + 2));
        continue;
      }
      if (rest.includes(":") && !rest.startsWith("{")) {
        const colon = rest.indexOf(":");
        const key = unquote(rest.slice(0, colon).trim());
        const value = rest.slice(colon + 1).trim();
        const item = {};
        if (value !== "") {
          item[key] = scalar(value);
        } else {
          item[key] = parseValue(indent + 2);
        }
        const more = peek();
        if (more != null && indentOf(more) > indent && !more.trim().startsWith("- ")) {
          Object.assign(item, parseMap(indentOf(more)));
        }
        arr.push(item);
        continue;
      }
      arr.push(scalar(rest));
    }
    return arr;
  }

  return parseValue(0);
}

function scalar(raw) {
  if (raw === "true") return true;
  if (raw === "false") return false;
  if (raw === "null" || raw === "~") return null;
  if (/^-?\d+$/.test(raw)) return Number(raw);
  return unquote(raw);
}

function unquote(value) {
  if (
    (value.startsWith('"') && value.endsWith('"')) ||
    (value.startsWith("'") && value.endsWith("'"))
  ) {
    return value.slice(1, -1);
  }
  return value;
}
