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
  path: string;
};

export type FileList = {
  path: string;
  entries: FileEntry[];
};
