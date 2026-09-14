import type { StoragePool } from "../../api/phase3";
import { formatBytes } from "../../format";
import { kindLabel } from "../../labels";

export function poolOptionLines(pool: StoragePool): string[] {
  const lines = [
    `${kindLabel(pool.backend_type)} · ${pool.usable_bytes == null ? "Capacity not reported" : `${formatBytes(pool.usable_bytes)} available`}`,
    pool.capabilities?.snapshots ? "Snapshots available" : "Snapshots unavailable",
  ];
  const warning = pool.warning_text?.[0];
  if (warning) {
    lines.push(warning);
  }
  return lines;
}

export function StoragePicker({
  id,
  label,
  pools,
  value,
  onChange,
  expert,
}: {
  id: string;
  label: string;
  pools: StoragePool[];
  value: string;
  onChange: (id: string) => void;
  expert?: boolean;
}) {
  const selected = pools.find((p) => p.id === value);
  return (
    <div className="field">
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      <select id={id} className="field-input" value={value} onChange={(e) => onChange(e.target.value)}>
        {pools.map((pool) => (
          <option key={pool.id} value={pool.id}>
            {pool.name}
            {expert ? ` (${pool.id})` : ""}
          </option>
        ))}
      </select>
      {selected ? (
        <div className="picker-option">
          {poolOptionLines(selected).map((line) => (
            <p key={line} className="picker-meta">
              {line}
            </p>
          ))}
        </div>
      ) : null}
    </div>
  );
}
