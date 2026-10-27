import {
  chmodFile,
  chownFile,
  copyFile,
  deleteFile,
  downloadFile,
  listFiles,
  mkdirFile,
  moveFile,
  readFileContent,
  uploadFile,
  type UploadProgress,
} from "../api/client";
import type { FileContent, FileEntry } from "../api/phase6";
import {
  compressGameFiles,
  deleteGameFile,
  downloadGameFile,
  extractGameFile,
  listGameFiles,
  mkdirGameFile,
  moveGameFile,
  readGameFile,
  uploadGameFile,
} from "../gameservers/api";

export type UploadOptions = { expectedMtime?: string; signal?: AbortSignal; onProgress?: (p: UploadProgress) => void };

/**
 * FilesBackend is what the Files browser and editor call. The agent's
 * file API (nodes and workloads) supports everything; a game server's data
 * volume has no copy, permissions or owner, so those are left out and the
 * browser hides them.
 */
export type FilesBackend = {
  list: (path: string) => Promise<FileEntry[]>;
  read: (path: string) => Promise<FileContent>;
  mkdir: (path: string) => Promise<void>;
  remove: (path: string, expectedMtime?: string) => Promise<void>;
  move: (from: string, to: string) => Promise<void>;
  upload: (path: string, file: File, opts?: UploadOptions) => Promise<void>;
  download: (path: string, filename: string) => Promise<void>;
  copy?: (from: string, to: string) => Promise<void>;
  chmod?: (path: string, mode: number) => Promise<void>;
  chown?: (path: string, uid: number, gid: number) => Promise<void>;
  /** extract unpacks a .zip, .tar, .tar.gz or .tgz into dest (a directory). */
  extract?: (path: string, dest: string) => Promise<void>;
  /** compress zips paths into dest and returns the archive's path. */
  compress?: (paths: string[], dest: string) => Promise<string>;
};

/** isArchive reports whether extract can unpack a file of this name. */
export const isArchive = (name: string) => /\.(zip|tar|tar\.gz|tgz)$/i.test(name);

export function agentFilesBackend(kind: "node" | "workload", id: string): FilesBackend {
  return {
    list: async (path) => (await listFiles(kind, id, path)).entries ?? [],
    read: (path) => readFileContent(kind, id, path),
    mkdir: (path) => mkdirFile(kind, id, path),
    remove: (path, mtime) => deleteFile(kind, id, path, mtime),
    move: (from, to) => moveFile(kind, id, from, to),
    upload: (path, file, opts) => uploadFile(kind, id, path, file, opts),
    download: (path, filename) => downloadFile(kind, id, path, filename),
    copy: (from, to) => copyFile(kind, id, from, to),
    chmod: (path, mode) => chmodFile(kind, id, path, mode),
    chown: (path, uid, gid) => chownFile(kind, id, path, uid, gid),
  };
}

/** The game API takes paths relative to the server's data volume. */
const rel = (path: string) => path.replace(/^\/+/, "");
const base = (path: string) => path.split("/").filter(Boolean).pop() ?? path;

// The editor opens files up to 2 MiB, the same limit the game API reads.
const EDITOR_LIMIT = 2 << 20;

export function gameFilesBackend(id: string): FilesBackend {
  return {
    list: async (path) => {
      const listed = await listGameFiles(id, rel(path));
      return (listed.items ?? []).map((e) => ({
        name: e.name,
        type: e.dir ? "dir" : "file",
        size: e.size,
        mtime: e.modified_at || undefined,
        path: `/${[rel(path), e.name].filter(Boolean).join("/")}`,
      }));
    },
    read: async (path) => {
      const name = base(path);
      try {
        const body = await readGameFile(id, rel(path));
        // The game API returns raw text; a NUL byte means the file is binary.
        const binary = body.content.includes("\u0000");
        return { name, type: "file", size: body.content.length, path: `/${rel(body.path || path)}`, content: binary ? undefined : body.content, binary, editable: !binary };
      } catch (err) {
        // Files over the read limit stay download-only, as on workloads.
        if (err instanceof Error && /too large|exceed|limit/i.test(err.message)) {
          return { name, type: "file", size: EDITOR_LIMIT + 1, path, too_large: true };
        }
        throw err;
      }
    },
    mkdir: async (path) => {
      await mkdirGameFile(id, rel(path));
    },
    remove: async (path) => {
      await deleteGameFile(id, rel(path));
    },
    move: async (from, to) => {
      await moveGameFile(id, rel(from), rel(to));
    },
    upload: (path, file, opts) => uploadGameFile(id, rel(path), file, opts?.signal),
    download: (path, filename) => downloadGameFile(id, rel(path), filename),
    extract: async (path, dest) => {
      await extractGameFile(id, rel(path), rel(dest));
    },
    compress: async (paths, dest) => `/${(await compressGameFiles(id, paths.map(rel), rel(dest))).path}`,
  };
}
