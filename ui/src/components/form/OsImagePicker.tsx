import { osLabel } from "../../labels";

export function OsImagePicker({
  id,
  label,
  pins,
  value,
  onChange,
  expert,
}: {
  id: string;
  label: string;
  pins: string[];
  value: string;
  onChange: (pin: string) => void;
  expert?: boolean;
}) {
  return (
    <div className="field">
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      {expert ? (
        <input
          id={id}
          className="field-input"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          spellCheck={false}
        />
      ) : (
        <select id={id} className="field-input" value={value} onChange={(e) => onChange(e.target.value)}>
          {pins.map((pin) => (
            <option key={pin} value={pin}>
              {osLabel(pin)}
            </option>
          ))}
        </select>
      )}
    </div>
  );
}
