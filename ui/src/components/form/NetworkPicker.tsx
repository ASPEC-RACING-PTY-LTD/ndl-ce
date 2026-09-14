import type { Network } from "../../api/phase4";
import { kindLabel } from "../../labels";

export function networkIsUsable(net: Network): boolean {
  return net.status === "available" || net.status === "warning";
}

export function networkIsGuestSafe(net: Network): boolean {
  if (net.danger === "dangerous") {
    return false;
  }
  return net.kind !== "lan-bridge";
}

export function preferredGuestNetwork(networks: Network[]): Network | undefined {
  const usable = networks.filter(networkIsUsable);
  return usable.find(networkIsGuestSafe) ?? usable[0];
}

export function sortGuestNetworks(networks: Network[]): Network[] {
  return [...networks].sort((a, b) => {
    const safe = Number(networkIsGuestSafe(b)) - Number(networkIsGuestSafe(a));
    if (safe !== 0) {
      return safe;
    }
    return a.name.localeCompare(b.name);
  });
}

export function networkOptionLines(net: Network): string[] {
  const lines = [kindLabel(net.kind)];
  if (net.bridge_name) {
    lines.push(`Bridge ${net.bridge_name}`);
  }
  lines.push(net.dhcp ? "DHCP on" : "DHCP off");
  if (net.danger === "dangerous") {
    lines.push(net.reason || "This change can affect the management network.");
  }
  const warning = net.warnings?.[0];
  if (warning) {
    lines.push(warning);
  }
  return lines;
}

export function NetworkPicker({
  id,
  label,
  networks,
  value,
  onChange,
  expert,
}: {
  id: string;
  label: string;
  networks: Network[];
  value: string;
  onChange: (id: string) => void;
  expert?: boolean;
}) {
  const selected = networks.find((n) => n.id === value);
  return (
    <div className="field">
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      <select id={id} className="field-input" value={value} onChange={(e) => onChange(e.target.value)}>
        {sortGuestNetworks(networks).map((net) => (
          <option key={net.id} value={net.id}>
            {net.name}
            {expert ? ` (${net.id})` : ""}
          </option>
        ))}
      </select>
      {selected ? (
        <div className="picker-option">
          {networkOptionLines(selected).map((line) => (
            <p key={line} className="picker-meta">
              {line}
            </p>
          ))}
        </div>
      ) : null}
    </div>
  );
}
