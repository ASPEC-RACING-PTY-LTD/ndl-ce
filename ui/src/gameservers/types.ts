export type GameCapability =
  | "console"
  | "files"
  | "config"
  | "startup"
  | "network"
  | "resources"
  | "backups"
  | "schedules"
  | "mods"
  | "plugins"
  | "worlds"
  | "players"
  | "databases"
  | "eula"
  | "rcon"
  | "steamcmd"
  | "java"
  | "license_key"
  | "workshop"
  | "query";

export type GamePort = {
  name?: string;
  container_port: number;
  host_port?: number;
  protocol?: string;
  primary?: boolean;
};

export type GameServer = {
  id: string;
  name: string;
  status: string;
  desired_power?: string;
  template_id: string;
  template_name?: string;
  game: string;
  implementation?: string;
  family?: string;
  node_id?: string;
  cpus: number;
  memory_bytes: number;
  disk_bytes: number;
  ports?: GamePort[];
  capabilities?: string[];
  pinned?: boolean;
  created_at?: string;
  updated_at?: string;
  install_phase?: string;
  error_human?: string;
  error_raw?: string;
  image_label?: string;
  notes?: string;
  env?: Record<string, string>;
  startup?: string;
  image?: string;
  owner_user_id?: string;
  install_log?: string;
};

export type GameTemplate = {
  id: string;
  name: string;
  game: string;
  implementation?: string;
  family?: string;
  summary?: string;
  capabilities?: string[];
  images?: Record<string, string>;
  default_image?: string;
  variables?: GameVariable[];
  friendly_config?: GameSetting[];
  default_ports?: GamePort[];
  default_memory_mb?: number;
  default_disk_mb?: number;
  default_cpus?: number;
  tags?: string[];
  aliases?: string[];
  start_requires?: string[];
  hint?: string;
  content?: { provider?: string; kind?: string; install_dir?: string };
};

export type GameVariable = {
  name: string;
  env: string;
  description?: string;
  default?: string;
  viewable?: boolean;
  editable?: boolean;
  required?: boolean;
  secret?: boolean;
  field_type?: string;
};

export type GameSetting = {
  id: string;
  label: string;
  help?: string;
  kind: string;
  file?: string;
  key?: string;
  env?: string;
  default?: string;
  options?: string[];
  restart?: boolean;
  min?: number;
  max?: number;
  advanced?: boolean;
};

export type CatalogueItem = {
  id: string;
  name: string;
  game: string;
  implementation?: string;
  family?: string;
  summary?: string;
  source?: string;
  import_url?: string;
  builtin?: boolean;
  tags?: string[];
  capabilities?: string[];
  aliases?: string[];
  runtime_kind?: string;
  hint?: string;
};

export type GameContentItem = {
  id?: string;
  provider?: string;
  external_id?: string;
  name: string;
  slug?: string;
  version?: string;
  summary?: string;
  enabled?: boolean;
  filename?: string;
  dependencies?: string[];
  incompatible?: boolean;
  source_url?: string;
};

export type GameView = "grid" | "compact" | "list" | "large";
