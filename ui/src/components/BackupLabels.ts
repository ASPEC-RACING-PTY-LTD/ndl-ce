import { honestStatus } from "../format";
import type { BackupArtifact, BackupRun, BackupTarget } from "../generated/openapi";

// Labels and tones shared by the Backups page and its workload table.

export function targetStatusLabel(status: BackupTarget["status"]): string {
  switch (status) {
    case "available":
      return "Available";
    case "unavailable":
      return "Unavailable";
    case "not_configured":
      return "Not configured";
    case "untested":
      return "Untested";
    case "authentication_failed":
      return "Authentication failed";
    case "permission_denied":
      return "Permission denied";
    case "bucket_unavailable":
      return "Bucket unavailable";
    case "degraded":
      return "Degraded";
    default:
      return honestStatus(status);
  }
}

export function targetTone(status: BackupTarget["status"]): string {
  switch (status) {
    case "available":
      return "is-ok";
    case "untested":
    case "not_configured":
    case "degraded":
      return "is-warn";
    default:
      return "is-danger";
  }
}

export function runStatusLabel(status: BackupRun["status"]): string {
  switch (status) {
    case "running":
      return "Running";
    case "succeeded":
      return "Succeeded";
    case "succeeded_with_warnings":
      return "Succeeded with warnings";
    case "failed":
      return "Failed";
    case "interrupted":
      return "Interrupted";
    default:
      return honestStatus(status);
  }
}

export function runTone(status: BackupRun["status"]): string {
  switch (status) {
    case "succeeded":
      return "is-ok";
    case "running":
      return "is-active";
    case "failed":
    case "interrupted":
      return "is-danger";
    case "succeeded_with_warnings":
      return "is-warn";
    default:
      return "";
  }
}

export function protectionLabel(art: BackupArtifact): string {
  if (art.protection) {
    return art.protection;
  }
  if (art.format !== "ndl-cab" && art.format !== "zfs" && art.format !== "qcow2") {
    return art.verify_status === "verified" ? "Legacy verified" : "Legacy";
  }
  switch (art.remote_state) {
    case "queued":
      return "Queued";
    case "uploading":
      return "Uploading";
    case "verifying":
      return "Remote verifying";
    case "protected":
      return "Protected";
    case "failed":
      return "Failed";
    default:
      return art.local_complete ? "Local complete" : "Capturing";
  }
}

export type ProtectionBucket = "protected" | "localPending" | "queued" | "uploading" | "failed" | "legacy";

export function protectionBucket(art: BackupArtifact): ProtectionBucket {
  switch (protectionLabel(art)) {
    case "Protected":
      return "protected";
    case "Queued":
      return "queued";
    case "Uploading":
    case "Remote verifying":
      return "uploading";
    case "Failed":
      return "failed";
    case "Legacy":
    case "Legacy verified":
      return "legacy";
    default:
      return "localPending";
  }
}

export function protectionTone(art: BackupArtifact): string {
  switch (protectionBucket(art)) {
    case "protected":
      return "is-ok";
    case "failed":
      return "is-danger";
    case "uploading":
    case "queued":
      return "is-active";
    case "localPending":
      return "is-warn";
    default:
      return "";
  }
}

/** recoveryLabel names a workload's disaster recovery state. */
export function recoveryLabel(status: string | undefined): string {
  switch (status) {
    case "recoverable":
      return "Recoverable";
    case "stale":
      return "Stale";
    case "missing":
      return "Missing";
    default:
      return status ? honestStatus(status) : "Unknown";
  }
}

export function recoveryTone(status: string | undefined): string {
  return status === "recoverable" ? "is-ok" : status === "stale" ? "is-warn" : "is-danger";
}
