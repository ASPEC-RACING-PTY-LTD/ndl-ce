import { useEffect, useState } from "react";
import { applyNetwork, createNetwork, listNetworks } from "../api/client";
import type { ConfirmRequired, Network, NetworkNIC, NetworkPreview } from "../api/phase4";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ErrorState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { SelectField } from "../components/form/SelectField";
import { PageHeader } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { StatusBadge } from "../components/StatusBadge";
import { kindLabel } from "../labels";
import { canMutate, mutateHint } from "../rbac";
import { useSession } from "../session";

function isConfirm(value: unknown): value is ConfirmRequired {
  return Boolean(value && typeof value === "object" && "code" in value && (value as ConfirmRequired).code === "confirmation_required");
}

function previewLines(result: Network | NetworkPreview): string[] {
  const lines: string[] = [];
  if ("kind" in result && result.kind) {
    lines.push(`Type: ${kindLabel(result.kind)}`);
  }
  if ("bridge_name" in result && result.bridge_name) {
    lines.push(`Bridge: ${result.bridge_name}`);
  }
  if ("dhcp" in result && result.dhcp != null) {
    lines.push(result.dhcp ? "DHCP: on" : "DHCP: off");
  }
  if ("nat" in result && result.nat != null) {
    lines.push(result.nat ? "NAT: on" : "NAT: off");
  }
  if ("danger_reason" in result && result.danger_reason) {
    lines.push(result.danger_reason);
  }
  if ("warnings" in result && result.warnings) {
    lines.push(...result.warnings);
  }
  return lines;
}

export function NetworkPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [items, setItems] = useState<Network[] | null>(null);
  const [nics, setNics] = useState<NetworkNIC[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("isolated");
  const [kind, setKind] = useState("isolated");
  const [cidr, setCidr] = useState("10.64.0.0/24");
  const [uplink, setUplink] = useState("");
  const [typed, setTyped] = useState("");
  const [confirmToken, setConfirmToken] = useState("");
  const [preview, setPreview] = useState<string[] | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [confirmMessage, setConfirmMessage] = useState("");
  const [pendingApply, setPendingApply] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function reload() {
    const listed = await listNetworks();
    setItems(listed.items ?? []);
    setNics(listed.nics ?? []);
  }

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
        setItems([]);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const firstRun = (items?.length ?? 0) === 0;

  async function onDryRun() {
    setBusy(true);
    setError(null);
    try {
      const result = await createNetwork({
        name,
        kind,
        ipv4_cidr: kind === "lan-bridge" ? undefined : cidr,
        uplink_ifname: kind === "lan-bridge" ? uplink : undefined,
        dry_run: true,
      });
      setPreview(previewLines(result as NetworkPreview));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Dry-run failed");
    } finally {
      setBusy(false);
    }
  }

  async function finishCreate() {
    const result = await createNetwork(
      {
        name,
        kind,
        ipv4_cidr: kind === "lan-bridge" ? undefined : cidr,
        uplink_ifname: kind === "lan-bridge" ? uplink : undefined,
        confirm_ifname: typed || undefined,
      },
      confirmToken || undefined,
    );
    if (isConfirm(result)) {
      setConfirmToken(result.confirm_token ?? "");
      setTyped(result.typed_ifname ?? uplink);
      setConfirmMessage(result.message ?? "Type the interface name to confirm this change.");
      setConfirmOpen(true);
      return;
    }
    setConfirmToken("");
    setConfirmOpen(false);
    await reload();
  }

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
      await finishCreate();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onApply(id: string) {
    setBusy(true);
    setError(null);
    try {
      const result = await applyNetwork(id, false, typed || undefined, confirmToken || undefined);
      if (isConfirm(result)) {
        setPendingApply(id);
        setConfirmToken(result.confirm_token ?? "");
        setTyped(result.typed_ifname ?? uplink);
        setConfirmMessage(result.message ?? "Type the interface name to confirm this change.");
        setConfirmOpen(true);
        return;
      }
      setPendingApply(null);
      setConfirmOpen(false);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Apply failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page" aria-labelledby="network-heading">
      <PageHeader
        id="network-heading"
        title="Network"
        kicker="Isolated, isolated with NAT, and LAN bridge. Isolated is the safe default."
      />
      {error ? <ErrorState>{error}</ErrorState> : null}
      {firstRun ? (
        <p className="banner banner-warn" role="status">
          No guest network yet. Create an isolated network so workloads have L2 without touching the
          management NIC.
        </p>
      ) : null}
      {firstRun || mutate ? (
        <article className="panel form-narrow">
          <h2>{firstRun ? "First-run guest network" : "Create network"}</h2>
          <div className="form">
            <Field id="net-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
            <SelectField id="net-kind" label="Type" value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="isolated">Isolated (DHCP on a No-dal bridge)</option>
              <option value="isolated-nat">Isolated with NAT</option>
              <option value="lan-bridge">LAN bridge (no DHCP)</option>
            </SelectField>
            {kind !== "lan-bridge" ? (
              <Field id="net-cidr" label="IPv4 CIDR" value={cidr} onChange={(e) => setCidr(e.target.value)} />
            ) : (
              <>
                <Field
                  id="net-uplink"
                  label="Uplink interface"
                  value={uplink}
                  onChange={(e) => setUplink(e.target.value)}
                  hint="LAN bridge never starts a second DHCP server."
                />
                <Field
                  id="net-typed"
                  label="Type the interface name to confirm"
                  value={typed}
                  onChange={(e) => setTyped(e.target.value)}
                  hint="Required when the uplink is the management NIC or the only physical NIC."
                />
              </>
            )}
            {mutateHint(roles) ? <p className="field-hint">{mutateHint(roles)}</p> : null}
            <div className="btn-row">
              <button className="btn" type="button" disabled={busy} onClick={() => void onDryRun()}>
                Dry-run
              </button>
              <button className="btn btn-primary" type="button" disabled={busy || !mutate} onClick={() => void onCreate()}>
                Create network
              </button>
            </div>
            {preview && preview.length > 0 ? (
              <div className="review-box">
                {preview.map((line) => (
                  <p key={line}>{line}</p>
                ))}
              </div>
            ) : null}
          </div>
        </article>
      ) : null}
      <article className="panel">
        <h2>Networks</h2>
        {items == null ? (
          <p role="status" aria-busy="true">
            Loading
          </p>
        ) : (
          <ResourceTable
            headers={["Name", "Type", "Status", "Bridge", "DHCP", "Actions"]}
            empty={<p>No network objects are recorded yet.</p>}
            rows={items.map((net) => [
              net.name,
              kindLabel(net.kind),
              <span key="st">
                <StatusBadge status={net.status} />
                {net.danger === "dangerous" ? (
                  <span className="picker-meta"> {net.reason || "Can affect the management network"}</span>
                ) : null}
              </span>,
              net.bridge_name || "Not reported",
              net.dhcp ? "On" : "Off",
              mutate ? (
                <button key="ap" className="btn btn-ghost btn-sm" type="button" onClick={() => void onApply(net.id)}>
                  Apply
                </button>
              ) : (
                ""
              ),
            ])}
          />
        )}
      </article>
      <article className="panel">
        <h2>Host NICs</h2>
        <ResourceTable
          headers={["Name", "Index", "State", "Addresses"]}
          empty={<p>NIC inventory is unavailable.</p>}
          rows={nics.map((nic) => [
            nic.name,
            nic.ifindex ?? "Not reported",
            nic.state || "Not reported",
            nic.addresses?.join(", ") || "Not reported",
          ])}
        />
      </article>
      <ConfirmDialog
        open={confirmOpen}
        title="Confirm network change"
        confirmLabel="Confirm"
        onClose={() => setConfirmOpen(false)}
        onConfirm={() => {
          if (pendingApply) {
            void onApply(pendingApply);
          } else {
            void onCreate();
          }
        }}
      >
        <p>{confirmMessage}</p>
        <Field id="confirm-if" label="Interface name" value={typed} onChange={(e) => setTyped(e.target.value)} />
      </ConfirmDialog>
    </section>
  );
}
