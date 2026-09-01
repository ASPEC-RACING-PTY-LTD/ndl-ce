export type StoragePool = {
  id: string;
  name: string;
  backend_type: string;
  status: string;
  reason?: string;
  locator?: string;
  warnings?: string[];
  warning_text?: string[];
  capabilities?: {
    incremental_send?: boolean;
    snapshots?: boolean;
    volume_create?: boolean;
    sparse_files?: boolean;
    xattr_identity?: boolean;
    shared_warning?: boolean;
    supported_classes?: string[];
  };
  usable_bytes?: number | null;
  allocated_bytes?: number | null;
  provisioned_bytes?: number | null;
  total_bytes?: number | null;
  storage_classes?: string[];
  adopted?: boolean;
};

export type StorageVolume = {
  id: string;
  pool_id: string;
  class: string;
  kind: string;
  format: string;
  size_bytes: number;
  status: string;
  backend_type: string;
  backend_ref: string;
  xattr_state?: string;
  allocated_bytes?: number | null;
};

export type LibraryItem = {
  id: string;
  pool_id: string;
  kind: string;
  display_name: string;
  backend_ref: string;
  size_bytes: number;
  checksum_sha256: string;
  status: string;
  created_at?: string;
};

export type PoolListResponse = {
  items: StoragePool[];
  default_path?: string;
};
