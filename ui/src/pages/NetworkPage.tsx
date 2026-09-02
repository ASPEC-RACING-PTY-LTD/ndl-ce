import { useEffect, useState } from "react";
import { applyNetwork, applyPolicy, createBond, createNetwork, createPolicy, createVLAN, listNetworks } from "../api/client";
import type { ConfirmRequired, Network, NetworkBond, NetworkNIC, NetworkPolicy, NetworkVLAN } from "../api/phase4";
import { Field } from "../components/Field";
import { PageHeader } from "../components/PageHeader";
import { useSession } from "../session";

function canMutate(roles: string[] | undefined): boolean {
  return Boolean(roles?.includes("admin") || roles?.includes("operator"));
}

function isConfirm(value: unknown): value is ConfirmRequired {
  return Boolean(value && typeof value === "object" && "code" in value && (value as ConfirmRequired).code === "confirmation_required");
}

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
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Policy apply failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="page page-wide" aria-labelledby="network-heading">
      <PageHeader
        id="network-heading"
        title="Network"
        kicker="Isolated, isolated-NAT, and LAN-bridge networks. VLAN access ports, bonds, and guest policies are optional. Isolated is the safe default. Policies cannot drop management INPUT."
      />
      {error ? (
        <p className="banner banner-error" role="alert">
          {error}
        </p>
      ) : null}
      {firstRun ? (
        <p className="banner banner-warn" role="status">
          No guest network yet. Create an isolated network so later workloads have L2 without touching the management NIC.
        </p>
      ) : null}
      {firstRun || mutate ? (
        <article className="panel">
          <h2>{firstRun ? "First-run guest network" : "Create network"}</h2>
          <Field id="net-name" label="Name" value={name} onChange={(e) => setName(e.target.value)} />
          <div className="field">
            <label className="field-label" htmlFor="net-kind">
              Kind
            </label>
            <select id="net-kind" className="field-input" value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="isolated">isolated (DHCP on a No-dal bridge)</option>
              <option value="isolated-nat">isolated-nat (isolated plus masquerade)</option>
              <option value="lan-bridge">lan-bridge (enslave a NIC, no DHCP)</option>
            </select>
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
        </article>
      ) : null}
      <article className="panel">
        <h2>Networks</h2>
        {items.length === 0 ? (
          <p>Collecting. No network objects are recorded yet.</p>
        ) : (
          <ul className="plain-list">
            {items.map((net) => (
              <li key={net.id}>
                <strong>{net.name}</strong> {net.kind} {net.status}
                {net.bridge_name ? ` locator ${net.bridge_name}` : ""}
                {net.dhcp ? " DHCP on" : " DHCP off"}
                {net.danger === "dangerous" ? " dangerous" : ""}
                {mutate ? (
                  <button className="btn btn-ghost" type="button" onClick={() => void onApply(net.id)}>
                    Apply
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </article>
      {mutate ? (
        <article className="panel">
          <h2>VLANs, bonds, and policies</h2>
          <p className="lede">
            Stacked VLAN access ports use a VID such as 20. Bonds default to active-backup. Guest
            policies use the nftables bridge family and cannot drop management INPUT. The 120s
            rollback watchdog still wins on dangerous uplink changes.
          </p>
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onCreateVLAN();
            }}
          >
            <Field id="vlan-vid" label="VLAN ID" value={vlanVid} onChange={(e) => setVlanVid(e.target.value)} hint="Access port PVID. 20 is the homelab example." />
            <Field id="vlan-access" label="Access interface" value={vlanAccess} onChange={(e) => setVlanAccess(e.target.value)} hint="Optional extra NIC. Management requires typed confirm." />
            <button className="btn btn-primary" type="submit" disabled={busy}>
              Add VLAN
            </button>
          </form>
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onCreateBond();
            }}
          >
            <Field id="bond-name" label="Bond name" value={bondName} onChange={(e) => setBondName(e.target.value)} />
            <Field id="bond-members" label="Members" value={bondMembers} onChange={(e) => setBondMembers(e.target.value)} hint="Comma-separated extra NICs, for example eth1,eth2." />
            <button className="btn" type="submit" disabled={busy || !bondMembers}>
              Add bond
            </button>
          </form>
          <form
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              void onCreatePolicy();
            }}
          >
            <Field id="pol-name" label="Policy name" value={polName} onChange={(e) => setPolName(e.target.value)} />
            <Field id="pol-src" label="Source workload UUID" value={polSrc} onChange={(e) => setPolSrc(e.target.value)} />
            <Field id="pol-dst" label="Destination workload UUID" value={polDst} onChange={(e) => setPolDst(e.target.value)} />
            <button className="btn" type="submit" disabled={busy || !polSrc || !polDst}>
              Deny pair
            </button>
          </form>
          {vlans.length > 0 ? (
            <ul className="plain-list">
              {vlans.map((v) => (
                <li key={v.id}>
                  VLAN {v.vlan_id} {v.locator} {v.status}
                </li>
              ))}
            </ul>
          ) : null}
          {bonds.length > 0 ? (
            <ul className="plain-list">
              {bonds.map((b) => (
                <li key={b.id}>
                  Bond {b.name} {b.mode} {b.locator} {b.status}
                </li>
              ))}
            </ul>
          ) : null}
          {policies.length > 0 ? (
            <ul className="plain-list">
              {policies.map((p) => (
                <li key={p.id}>
                  Policy {p.name} {p.action} {p.status}
                </li>
              ))}
            </ul>
          ) : null}
        </article>
      ) : null}
      <article className="panel">
        <h2>Host NICs</h2>
        {nics.length === 0 ? (
          <p>NIC inventory is Collecting or unavailable.</p>
        ) : (
          <ul className="plain-list">
            {nics.map((nic) => (
              <li key={nic.name}>
                {nic.name} ifindex {nic.ifindex ?? "not reported"} {nic.state ?? ""} {nic.addresses?.join(", ") ?? ""}
              </li>
            ))}
          </ul>
        )}
      </article>
    </section>
  );
}
