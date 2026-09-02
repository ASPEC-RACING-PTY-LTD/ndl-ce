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
  vlans?: NetworkVLAN[];
  bonds?: NetworkBond[];
  policies?: NetworkPolicy[];
  overlays?: NetworkOverlay[];
  first_run?: boolean;
};

export type NetworkVLAN = {
  id: string;
  network_id?: string;
  name?: string;
  vlan_id?: number;
  parent_ifname?: string;
  access_ifname?: string;
  mode?: string;
  locator?: string;
  status?: string;
  reason?: string;
};

export type NetworkBond = {
  id: string;
  name: string;
  mode?: string;
  members?: string[];
  locator?: string;
  status?: string;
  reason?: string;
};

export type NetworkPolicy = {
  id: string;
  name: string;
  action?: string;
  src_workload_id?: string;
  dst_workload_id?: string;
  src_mac?: string;
  dst_mac?: string;
  status?: string;
  reason?: string;
};

export type NetworkOverlay = {
  id: string;
  name?: string;
  vni?: number;
  locator?: string;
  status?: string;
  reason?: string;
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
