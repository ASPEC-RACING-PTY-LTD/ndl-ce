export type NetworkKind = "isolated" | "isolated-nat" | "lan-bridge";

export type Network = {
  id: string;
  name: string;
  kind: NetworkKind | string;
  status: string;
  reason?: string;
  danger?: string;
  bridge_name?: string;
  uplink_ifname?: string;
  ipv4_cidr?: string;
  gateway?: string;
  dhcp?: boolean;
  dns?: boolean;
  nat?: boolean;
  persist_kind?: string;
  warnings?: string[];
  management_ifindex?: number | null;
};

export type NetworkNIC = {
  name: string;
  ifindex?: number;
  mac?: string;
  state?: string;
  kind?: string;
  addresses?: string[];
};

export type NetworkListResponse = {
  items: Network[];
  nics?: NetworkNIC[];
  first_run?: boolean;
};

export type NetworkPreview = {
  network_id?: string;
  kind?: string;
  danger?: string;
  danger_reason?: string;
  requires_confirm?: boolean;
  typed_ifname?: string;
  dhcp?: boolean;
  dry_run?: boolean;
  warnings?: string[];
};

export type ConfirmRequired = {
  error: string;
  code?: string;
  danger?: string;
  typed_ifname?: string;
  confirm_token?: string;
  message?: string;
};

export type Reservation = {
  id: string;
  network_id: string;
  mac: string;
  ipv4: string;
  hostname?: string;
};
