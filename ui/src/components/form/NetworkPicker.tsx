import type { Network } from "../../api/phase4";
import { kindLabel } from "../../labels";

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
        {networks.map((net) => (
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
