import { useEffect, useState } from "react";
import { applyNetwork, applyPolicy, createBond, createNetwork, createPolicy, createVLAN, listNetworks } from "../api/client";
import type { ConfirmRequired, Network, NetworkBond, NetworkNIC, NetworkPolicy, NetworkVLAN } from "../api/phase4";
import { EmptyState } from "../components/EmptyState";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { useSession } from "../session";
import { Dialog } from "../ui/Dialog";
import { SelectionCard } from "../ui/SelectionCard";
import { kindLabel } from "../labels";

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

function isConfirm(value: unknown): value is ConfirmRequired {
  return Boolean(value && typeof value === "object" && "code" in value && (value as ConfirmRequired).code === "confirmation_required");
}

type CreateKind = "network" | "vlan" | "bond" | "policy" | null;

export function NetworkPage() {
  const session = useSession();
  const roles = session.status === "ready" ? session.user?.roles : undefined;
  const mutate = canMutate(roles);
  const [items, setItems] = useState<Network[]>([]);
  const [nics, setNics] = useState<NetworkNIC[]>([]);
  const [vlans, setVlans] = useState<NetworkVLAN[]>([]);
  const [bonds, setBonds] = useState<NetworkBond[]>([]);
  const [policies, setPolicies] = useState<NetworkPolicy[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("isolated");
  const [kind, setKind] = useState("isolated");
  const [cidr, setCidr] = useState("10.64.0.0/24");
  const [uplink, setUplink] = useState("");
  const [typed, setTyped] = useState("");
  const [confirmToken, setConfirmToken] = useState("");
  const [preview, setPreview] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [vlanVid, setVlanVid] = useState("20");
  const [vlanAccess, setVlanAccess] = useState("");
  const [bondName, setBondName] = useState("uplink");
  const [bondMembers, setBondMembers] = useState("");
  const [polName, setPolName] = useState("deny-pair");
  const [polSrc, setPolSrc] = useState("");
  const [polDst, setPolDst] = useState("");
  const [create, setCreate] = useState<CreateKind>(null);

  async function reload() {
    const listed = await listNetworks();
    setItems(listed.items ?? []);
    setNics(listed.nics ?? []);
    setVlans(listed.vlans ?? []);
    setBonds(listed.bonds ?? []);
    setPolicies(listed.policies ?? []);
  }

  useEffect(() => {
    let cancelled = false;
    void reload().catch((err) => {
      if (!cancelled) {
        setError(err instanceof Error ? err.message : "Unavailable");
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const firstRun = items.length === 0;

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
      setPreview(JSON.stringify(result, null, 2));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Dry-run failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreate() {
    setBusy(true);
    setError(null);
    try {
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
        setError(result.message ?? "Type the interface name to confirm this dangerous change.");
        return;
      }
      setConfirmToken("");
      setCreate(null);
      await reload();
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
        setConfirmToken(result.confirm_token ?? "");
        setTyped(result.typed_ifname ?? uplink);
        setError(result.message ?? "Type the interface name to confirm this dangerous change.");
        return;
      }
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Apply failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateVLAN() {
    setBusy(true);
    setError(null);
    try {
      await createVLAN({
        vlan_id: Number(vlanVid),
        network_id: items[0]?.id,
        access_ifname: vlanAccess || undefined,
        confirm_ifname: typed || undefined,
      });
      setCreate(null);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "VLAN add failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreateBond() {
    setBusy(true);
    setError(null);
    try {
      await createBond({
        name: bondName,
        mode: "active-backup",
        members: bondMembers.split(",").map((m) => m.trim()).filter(Boolean),
        confirm_ifname: typed || undefined,
      });
      setCreate(null);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Bond add failed");
    } finally {
      setBusy(false);
    }
  }

  async function onCreatePolicy() {
    setBusy(true);
    setError(null);
    try {
      const created = await createPolicy({
        name: polName,
        action: "deny",
        src_workload_id: polSrc,
        dst_workload_id: polDst,
      });
      await applyPolicy(created.id);
      setCreate(null);
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Policy apply failed");
    } finally {
      setBusy(false);
    }
  }

  const networkForm = (
    <div className="stack">
      <Field id="net-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
      <div className="content-grid" role="radiogroup" aria-label="Kind">
        {[
          { id: "isolated", title: "Isolated", desc: "DHCP on a No-DAL bridge" },
          { id: "isolated-nat", title: "Isolated NAT", desc: "Isolated plus masquerade" },
          { id: "lan-bridge", title: "LAN bridge", desc: "Enslave a NIC, no DHCP" },
        ].map((item) => (
          <SelectionCard
            key={item.id}
            role="radio"
            title={item.title}
            description={item.desc}
            selected={kind === item.id}
            onSelect={() => setKind(item.id)}
          />
        ))}
      </div>
      {kind !== "lan-bridge" ? (
        <Field id="net-cidr" label="IPv4 CIDR" value={cidr} onChange={(e) => setCidr(e.target.value)} />
      ) : (
        <>
          <Field
            id="net-uplink"
            label="Uplink interface"
            value={uplink}
            onChange={(e) => setUplink(e.target.value)}
            hint="LAN-bridge never starts a second DHCP server."
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
      <div className="btn-row">
        <button className="btn" type="button" disabled={busy} onClick={() => void onDryRun()}>
          Dry-run
        </button>
        <button className="btn btn-primary" type="button" disabled={busy || !mutate} onClick={() => void onCreate()}>
          Create network
        </button>
      </div>
      {preview ? <pre className="code-block">{preview}</pre> : null}
    </div>
  );

  return (
    <section className="page page-wide" aria-labelledby="network-heading">
      <PageHeader
        id="network-heading"
        title="Network"
        kicker="Guest networks first. Isolated is the safe default. Dangerous uplink changes still require typed confirmation."
        actions={
          mutate && !firstRun ? (
            <div className="btn-row is-flush">
              <button className="btn btn-primary" type="button" onClick={() => setCreate("network")}>
                Create network
              </button>
              <button className="btn btn-secondary" type="button" onClick={() => setCreate("vlan")}>
                VLAN
              </button>
              <button className="btn btn-secondary" type="button" onClick={() => setCreate("bond")}>
                Bond
              </button>
              <button className="btn btn-secondary" type="button" onClick={() => setCreate("policy")}>
                Policy
              </button>
            </div>
          ) : null
        }
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {firstRun ? (
        <article className="compact-card stack">
          <h2>First-run guest network</h2>
          <p className="lede">Create an isolated network so later workloads have L2 without touching the management NIC.</p>
          {networkForm}
        </article>
      ) : items.length === 0 ? (
        <EmptyState title="No networks">Create an isolated network to get started.</EmptyState>
      ) : (
        <div className="content-grid">
          {items.map((net) => (
            <article key={net.id} className="compact-card">
              <h3>{net.name}</h3>
              <p>
                {kindLabel(net.kind)} · {net.bridge_name || "no locator"}
              </p>
              <p className="field-hint">
                {net.ipv4_cidr || "No subnet"} · {net.dhcp ? "DHCP on" : "DHCP off"}
              </p>
              <StatusBadge status={net.status} />
              {net.danger === "dangerous" ? <p className="field-hint">Dangerous change</p> : null}
              {mutate ? (
                <button className="btn btn-ghost" type="button" onClick={() => void onApply(net.id)}>
                  {net.status === "available" ? "Re-apply" : "Apply"}
                </button>
              ) : null}
            </article>
          ))}
        </div>
      )}
      {vlans.length + bonds.length + policies.length > 0 ? (
        <div className="content-grid">
          {vlans.map((v) => (
            <article key={v.id} className="compact-card">
              <h3>VLAN {v.vlan_id}</h3>
              <p className="field-hint">{v.locator}</p>
              <StatusBadge status={v.status} />
            </article>
          ))}
          {bonds.map((b) => (
            <article key={b.id} className="compact-card">
              <h3>Bond {b.name}</h3>
              <p className="field-hint">
                {b.mode} · {b.locator}
              </p>
              <StatusBadge status={b.status} />
            </article>
          ))}
          {policies.map((p) => (
            <article key={p.id} className="compact-card">
              <h3>{p.name}</h3>
              <p className="field-hint">{p.action}</p>
              <StatusBadge status={p.status} />
            </article>
          ))}
        </div>
      ) : null}
      <article className="stack">
        <h2>Host NICs</h2>
        {nics.length === 0 ? (
          <p>NIC inventory is still collecting.</p>
        ) : (
          <div className="data-list">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>State</th>
                  <th>Index</th>
                  <th>Addresses</th>
                </tr>
              </thead>
              <tbody>
                {nics.map((nic) => (
                  <tr key={nic.name}>
                    <td>{nic.name}</td>
                    <td>{nic.state || "Not reported"}</td>
                    <td>{nic.ifindex != null ? `ifindex ${nic.ifindex}` : "Not reported"}</td>
                    <td>{nic.addresses?.join(", ") || "None"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </article>

      <Dialog open={!firstRun && create === "network"} title="Create network" wide onClose={() => setCreate(null)}>
        {networkForm}
      </Dialog>
      <Dialog open={create === "vlan"} title="Add VLAN" onClose={() => setCreate(null)}>
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            void onCreateVLAN();
          }}
        >
          <Field id="vlan-vid" label="VLAN ID" value={vlanVid} onChange={(e) => setVlanVid(e.target.value)} hint="Access port PVID." />
          <Field id="vlan-access" label="Access interface" value={vlanAccess} onChange={(e) => setVlanAccess(e.target.value)} hint="Optional extra NIC. Management requires typed confirm." />
          <div className="btn-row">
            <button className="btn btn-primary" type="submit" disabled={busy}>
              Add VLAN
            </button>
          </div>
        </form>
      </Dialog>
      <Dialog open={create === "bond"} title="Add bond" onClose={() => setCreate(null)}>
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            void onCreateBond();
          }}
        >
          <Field id="bond-name" label="Bond name" value={bondName} onChange={(e) => setBondName(e.target.value)} />
          <Field id="bond-members" label="Members" value={bondMembers} onChange={(e) => setBondMembers(e.target.value)} hint="Comma-separated extra NICs, for example eth1,eth2." />
          <div className="btn-row">
            <button className="btn btn-primary" type="submit" disabled={busy || !bondMembers}>
              Add bond
            </button>
          </div>
        </form>
      </Dialog>
      <Dialog open={create === "policy"} title="Add policy" onClose={() => setCreate(null)}>
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            void onCreatePolicy();
          }}
        >
          <Field id="pol-name" label="Policy name" value={polName} onChange={(e) => setPolName(e.target.value)} />
          <Field id="pol-src" label="Source workload" value={polSrc} onChange={(e) => setPolSrc(e.target.value)} />
          <Field id="pol-dst" label="Destination workload" value={polDst} onChange={(e) => setPolDst(e.target.value)} />
          <div className="btn-row">
            <button className="btn btn-primary" type="submit" disabled={busy || !polSrc || !polDst}>
              Deny pair
            </button>
          </div>
        </form>
      </Dialog>
    </section>
  );
}
