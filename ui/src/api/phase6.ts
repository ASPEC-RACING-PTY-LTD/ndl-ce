export type IOSession = {
  id: string;
  target_kind: string;
  target_id: string;
  kind: string;
  cwd: string;
  state: string;
  reason?: string;
  ticket?: string;
  jail_root?: string;
  ws_path?: string;
  expires_at?: string;
};

export type FileEntry = {
  name: string;
  type: string;
  size: number;
  mode?: number;
  mtime?: string;
  uid?: number;
  gid?: number;
  owner?: string;
  group?: string;
  path: string;
};

export type FileList = {
  path: string;
  entries: FileEntry[];
};

export type FileContent = {
  name: string;
  type: string;
  size: number;
  path: string;
  mode?: number;
  mtime?: string;
  uid?: number;
  gid?: number;
  owner?: string;
  group?: string;
  sha256?: string;
  encoding?: string;
  content?: string;
  binary?: boolean;
  too_large?: boolean;
  editable?: boolean;
};

export type FileMutation = {
  path: string;
  dest_path?: string;
  mode?: number;
  uid?: number;
  gid?: number;
  expected_mtime?: string;
  expected_sha256?: string;
};
