// Turns raw error text from the API, the agent or the browser into a short
// message a person can act on. The raw text is always kept as the detail, so
// nothing is hidden from someone debugging.

export type ExplainedError = {
  /** One plain sentence saying what went wrong. */
  summary: string;
  /** What to do about it, when known. */
  hint?: string;
  /** The original text, for the "Technical details" toggle. */
  detail: string;
};

type Rule = { match: RegExp; summary: string; hint?: string };

const RULES: Rule[] = [
  {
    match: /too many open files|EMFILE/i,
    summary: "The host ran out of open-file slots.",
    hint: "Restart the No-dal agent from the Node page, or reboot the host. If it keeps happening, a process is leaking file handles.",
  },
  {
    match: /fork\/exec \S*docker\S*: no such file|docker: (command )?not found|docker is not installed/i,
    summary: "Docker is not installed on this host.",
    hint: "Install the Docker feature from Add Features, then try again.",
  },
  {
    match: /no space left on device|ENOSPC|was stopped to protect the host|disk is (critically )?full/i,
    summary: "The host disk is full.",
    hint: "Free space under Storage, Host disk protection, then try again.",
  },
  {
    match: /^not authenticated$|unauthenticated|session (has )?expired|invalid session/i,
    summary: "Your session has expired.",
    hint: "Sign in again to continue.",
  },
  { match: /^forbidden$|permission to|not allowed to/i, summary: "You do not have permission to do this.", hint: "Ask an administrator for access." },
  {
    match: /agent is unavailable|agent unreachable|agent is not connected|dial unix .*agent|connection refused/i,
    summary: "The No-dal agent on the host is not responding.",
    hint: "It may be restarting. Wait a minute and try again.",
  },
  {
    match: /context deadline exceeded|timed? ?out|deadline exceeded/i,
    summary: "The operation took too long and was stopped.",
    hint: "Try again. If it keeps happening, check the host's load.",
  },
  {
    match: /failed to fetch|networkerror|network error|load failed|ECONNRESET/i,
    summary: "Could not reach No-dal.",
    hint: "Check your connection and try again.",
  },
  {
    match: /requires? the (container|vm|workload) to be stopped|stop the (container|vm|workload) (first|before)/i,
    summary: "Stop the workload first, then make this change.",
  },
  { match: /permission denied|EACCES|operation not permitted|EPERM/i, summary: "The host refused the operation (permission denied)." },
  { match: /already (exists|in use|taken)|duplicate key|is in use/i, summary: "That name or resource is already in use.", hint: "Pick another name, or remove the existing one first." },
  { match: /^request failed \(5\d\d\)$|internal server error/i, summary: "No-dal hit an internal error.", hint: "Try again. The technical details help when reporting it." },
  { match: /^request failed \(404\)$|^[\w\s-]* not found$/i, summary: "It no longer exists. It may have been deleted." },
];

// Prefixes that carry no meaning for a person: gRPC/Connect codes and Go
// error wrapping.
const NOISE = [
  /^rpc error: code = \w+ desc = /i,
  /^(failed_precondition|invalid_argument|internal|unavailable|unknown|not_found|already_exists|permission_denied|aborted|resource_exhausted|deadline_exceeded|unimplemented|canceled):\s*/i,
  /^(error|err):\s*/i,
];

function clean(text: string): string {
  let out = text.trim();
  for (let i = 0; i < 3; i++) {
    const before = out;
    for (const re of NOISE) {
      out = out.replace(re, "");
    }
    if (out === before) {
      break;
    }
  }
  // Keep the first line: package-manager or command output follows it.
  out = out.split("\n")[0].trim();
  if (out.length > 220) {
    out = `${out.slice(0, 217).trimEnd()}...`;
  }
  if (out) {
    out = out[0].toUpperCase() + out.slice(1);
  }
  if (out && !/[.!?]$/.test(out)) {
    out += ".";
  }
  return out;
}

/** explainError maps raw error text to a plain message plus the raw detail. */
export function explainError(error: unknown, fallback = "Something went wrong."): ExplainedError {
  const raw =
    typeof error === "string"
      ? error
      : error instanceof Error
        ? error.message
        : error == null
          ? ""
          : String(error);
  const detail = raw.trim();
  if (!detail) {
    return { summary: fallback, detail: "" };
  }
  for (const rule of RULES) {
    if (rule.match.test(detail)) {
      return { summary: rule.summary, hint: rule.hint, detail };
    }
  }
  return { summary: clean(detail) || fallback, detail };
}

/** errorText is the plain message only, for places that show one line. */
export function errorText(error: unknown, fallback = "Something went wrong."): string {
  return explainError(error, fallback).summary;
}
