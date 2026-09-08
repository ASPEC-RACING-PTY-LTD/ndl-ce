export type ContainerIPForm = {
  ipv4Mode: string;
  ipv4Address: string;
  ipv4Gateway: string;
  ipv6Mode: string;
  ipv6Address: string;
  ipv6Gateway: string;
  dns: string;
};

export const defaultContainerIPForm = (): ContainerIPForm => ({
  ipv4Mode: "dhcp",
  ipv4Address: "",
  ipv4Gateway: "",
  ipv6Mode: "disabled",
  ipv6Address: "",
  ipv6Gateway: "",
  dns: "",
});

export function containerIPBody(form: ContainerIPForm) {
  return {
    ipv4_mode: form.ipv4Mode,
    ipv4_address: form.ipv4Address,
    ipv4_gateway: form.ipv4Gateway,
    ipv6_mode: form.ipv6Mode,
    ipv6_address: form.ipv6Address,
    ipv6_gateway: form.ipv6Gateway,
    dns: form.dns
      .split(/[,\s]+/)
      .map((s) => s.trim())
      .filter(Boolean),
  };
}

export function summarizeContainerIP(form: ContainerIPForm): string {
  const family = (label: string, mode: string, addr: string, gw: string) => {
    if (mode === "static") {
      return gw ? `${label} Static ${addr} via ${gw}` : `${label} Static ${addr}`;
    }
    if (mode === "disabled") {
      return `${label} Disabled`;
    }
    return `${label} DHCP`;
  };
  const parts = [
    family("IPv4", form.ipv4Mode, form.ipv4Address, form.ipv4Gateway),
    family("IPv6", form.ipv6Mode, form.ipv6Address, form.ipv6Gateway),
  ];
  const dns = form.dns
    .split(/[,\s]+/)
    .map((s) => s.trim())
    .filter(Boolean);
  if (dns.length) {
    parts.push(`DNS ${dns.join(", ")}`);
  }
  return parts.join(", ");
}

const MODES = [
  { id: "dhcp", label: "DHCP" },
  { id: "static", label: "Static" },
  { id: "disabled", label: "Disabled" },
];

export function ContainerIPFields({
  id,
  form,
  onChange,
}: {
  id: string;
  form: ContainerIPForm;
  onChange: (next: ContainerIPForm) => void;
}) {
  const set = (patch: Partial<ContainerIPForm>) => onChange({ ...form, ...patch });
  return (
    <div className="stack">
      <FamilyFields
        id={`${id}-v4`}
        label="IPv4"
        mode={form.ipv4Mode}
        address={form.ipv4Address}
        gateway={form.ipv4Gateway}
        addressPlaceholder="192.168.1.10/24"
        gatewayPlaceholder="192.168.1.1"
        onMode={(ipv4Mode) => set({ ipv4Mode })}
        onAddress={(ipv4Address) => set({ ipv4Address })}
        onGateway={(ipv4Gateway) => set({ ipv4Gateway })}
      />
      <FamilyFields
        id={`${id}-v6`}
        label="IPv6"
        mode={form.ipv6Mode}
        address={form.ipv6Address}
        gateway={form.ipv6Gateway}
        addressPlaceholder="2001:db8::10/64"
        gatewayPlaceholder="2001:db8::1"
        onMode={(ipv6Mode) => set({ ipv6Mode })}
        onAddress={(ipv6Address) => set({ ipv6Address })}
        onGateway={(ipv6Gateway) => set({ ipv6Gateway })}
      />
      {form.ipv4Mode !== "disabled" || form.ipv6Mode !== "disabled" ? (
        <div className="field">
          <label className="field-label" htmlFor={`${id}-dns`}>
            DNS
          </label>
          <input
            id={`${id}-dns`}
            className="field-input"
            value={form.dns}
            placeholder="1.1.1.1, 8.8.8.8"
            onChange={(e) => set({ dns: e.target.value })}
          />
          <p className="field-hint">Optional. Used for static addressing, and written when provided.</p>
        </div>
      ) : null}
    </div>
  );
}

function FamilyFields({
  id,
  label,
  mode,
  address,
  gateway,
  addressPlaceholder,
  gatewayPlaceholder,
  onMode,
  onAddress,
  onGateway,
}: {
  id: string;
  label: string;
  mode: string;
  address: string;
  gateway: string;
  addressPlaceholder: string;
  gatewayPlaceholder: string;
  onMode: (mode: string) => void;
  onAddress: (addr: string) => void;
  onGateway: (gw: string) => void;
}) {
  return (
    <fieldset className="stack">
      <legend className="field-label">{label}</legend>
      <div className="field">
        <label className="field-label" htmlFor={`${id}-mode`}>
          {label} mode
        </label>
        <select id={`${id}-mode`} className="field-input" value={mode} onChange={(e) => onMode(e.target.value)}>
          {MODES.map((m) => (
            <option key={m.id} value={m.id}>
              {m.label}
            </option>
          ))}
        </select>
      </div>
      {mode === "static" ? (
        <div className="field-row">
          <div className="field">
            <label className="field-label" htmlFor={`${id}-addr`}>
              Address / CIDR
            </label>
            <input
              id={`${id}-addr`}
              className="field-input"
              value={address}
              placeholder={addressPlaceholder}
              onChange={(e) => onAddress(e.target.value)}
            />
          </div>
          <div className="field">
            <label className="field-label" htmlFor={`${id}-gw`}>
              Gateway
            </label>
            <input
              id={`${id}-gw`}
              className="field-input"
              value={gateway}
              placeholder={gatewayPlaceholder}
              onChange={(e) => onGateway(e.target.value)}
            />
          </div>
        </div>
      ) : null}
    </fieldset>
  );
}
