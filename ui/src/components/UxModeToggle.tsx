import type { UxLevel } from "../ux-mode";

const OPTIONS: { id: UxLevel; label: string }[] = [
  { id: "guided", label: "Guided" },
  { id: "advanced", label: "Advanced" },
  { id: "expert", label: "Expert" },
];

export function UxModeToggle({
  value,
  onChange,
}: {
  value: UxLevel;
  onChange: (next: UxLevel) => void;
}) {
  return (
    <div className="mode-toggle" role="group" aria-label="Configuration level">
      {OPTIONS.map((option) => (
        <button
          key={option.id}
          type="button"
          aria-pressed={value === option.id}
          onClick={() => onChange(option.id)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
