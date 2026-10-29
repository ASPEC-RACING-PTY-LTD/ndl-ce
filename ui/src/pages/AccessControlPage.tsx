import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ApiError,
  createGroup,
  deleteIAMGroup,
  deleteIAMRole,
  getIAMAccess,
  saveIAMGroup,
  saveIAMRole,
  setIAMRoleOrder,
  type IAMAccess,
  type IAMGroup,
  type IAMRole,
} from "../api/client";
import { confirmAction } from "../confirm";
import { LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Icon } from "../components/Icon";
import { AutomationCreatorsPanel } from "../iam/AutomationCreatorsPanel";
import { Dialog } from "../ui/Dialog";
import { permissionGroup, permissionLabel } from "../labels";

// Access control: roles, groups and the permissions they carry, edited in
// one place. Edits are kept per item while you move between items, then
// saved together. Roles are ordered top (highest) to bottom; the server
// stops anyone acting on a role at or above their own.

type RoleDraft = { kind: "role"; isNew: boolean; name: string; title: string; summary: string; login: boolean; permissions: string[] };
type GroupDraft = { kind: "group"; id: string; name: string; roles: string[]; members: string[] };
type Draft = RoleDraft | GroupDraft;

const roleKey = (name: string) => `role:${name}`;
const groupKey = (id: string) => `group:${id}`;

function roleDraft(r: IAMRole): RoleDraft {
  return { kind: "role", isNew: false, name: r.name, title: r.title, summary: r.summary, login: r.login, permissions: [...r.permissions] };
}

function groupDraft(g: IAMGroup): GroupDraft {
  return { kind: "group", id: g.id, name: g.name, roles: [...g.roles], members: [...g.member_ids] };
}

function sameList(a: string[], b: string[]): boolean {
  return a.length === b.length && [...a].sort().join("|") === [...b].sort().join("|");
}

function categoryOf(perm: string): string {
  return permissionGroup(perm);
}

export function AccessControlPage() {
  const [data, setData] = useState<IAMAccess | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [saving, setSaving] = useState(false);
  // The role order being edited in the list; null while it matches the server.
  const [order, setOrder] = useState<string[] | null>(null);
  const [drag, setDrag] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    try {
      const next = await getIAMAccess();
      setData(next);
      setError(null);
      setSelected((cur) => cur ?? (next.roles[0] ? roleKey(next.roles[0].name) : null));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unavailable");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const dirty = Object.keys(drafts).length;
  useEffect(() => {
    if (!dirty) {
      return;
    }
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  if (!data) {
    return error ? <ErrorNotice error={error} /> : <LoadingState label="Loading roles and groups" />;
  }

  const names = order ?? data.roles.map((r) => r.name);
  const roles = names.map((n) => data.roles.find((r) => r.name === n)).filter((r): r is IAMRole => Boolean(r));
  const groups = data.groups;
  const newRoles = Object.values(drafts).filter((d): d is RoleDraft => d.kind === "role" && d.isNew);
  // Root may place roles above SuperAdmin; everyone else orders what is below it.
  const firstMovable = data.can_order_above_top ? 0 : names.indexOf("superadmin") + 1;
  const movable = (i: number) => Boolean(data.can_order) && i >= firstMovable;

  function moveRole(name: string, to: number) {
    const next = names.filter((n) => n !== name);
    next.splice(Math.max(firstMovable, Math.min(to, next.length)), 0, name);
    setOrder(next.join("|") === data!.roles.map((r) => r.name).join("|") ? null : next);
  }

  async function saveOrder() {
    if (!order) {
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await setIAMRoleOrder(order);
      setOrder(null);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not save the order");
    } finally {
      setSaving(false);
    }
  }

  function current(key: string): Draft | null {
    if (drafts[key]) {
      return drafts[key];
    }
    if (key.startsWith("role:")) {
      const r = data!.roles.find((x) => roleKey(x.name) === key);
      return r ? roleDraft(r) : null;
    }
    const g = data!.groups.find((x) => groupKey(x.id) === key);
    return g ? groupDraft(g) : null;
  }

  function update(key: string, next: Draft) {
    setDrafts((cur) => {
      const base = key.startsWith("role:") ? data!.roles.find((x) => roleKey(x.name) === key) : null;
      const gbase = key.startsWith("group:") ? data!.groups.find((x) => groupKey(x.id) === key) : null;
      let unchanged = false;
      if (next.kind === "role" && base && !next.isNew) {
        unchanged = next.title === base.title && next.summary === base.summary && next.login === base.login && sameList(next.permissions, base.permissions);
      }
      if (next.kind === "group" && gbase) {
        unchanged = next.name === gbase.name && sameList(next.roles, gbase.roles) && sameList(next.members, gbase.member_ids);
      }
      const out = { ...cur };
      if (unchanged) {
        delete out[key];
      } else {
        out[key] = next;
      }
      return out;
    });
  }

  async function saveAll() {
    setSaving(true);
    setError(null);
    try {
      for (const [key, d] of Object.entries(drafts)) {
        if (d.kind === "role") {
          const id = d.isNew ? roleIDFor(d.title) || d.name : d.name;
          await saveIAMRole(id, { title: d.title, summary: d.summary, permissions: d.permissions, login: d.login });
        } else {
          await saveIAMGroup(d.id, { name: d.name, roles: d.roles, member_ids: d.members });
        }
        setDrafts((cur) => {
          const out = { ...cur };
          delete out[key];
          return out;
        });
      }
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }

  /** newRole adds an unsaved role draft; it is created with "Save all changes". */
  function newRole(title: string, summary: string): string | null {
    const name = roleIDFor(title);
    if (!name) {
      return "Give the role a name";
    }
    if (data?.roles.some((r) => r.name === name) || drafts[roleKey(name)]) {
      return `A role with the id "${name}" already exists`;
    }
    const key = roleKey(name);
    setDrafts((cur) => ({ ...cur, [key]: { kind: "role", isNew: true, name, title, summary, login: true, permissions: [] } }));
    setSelected(key);
    return null;
  }

  async function newGroup(name: string): Promise<string | null> {
    try {
      const g = await createGroup(name);
      await load();
      setSelected(groupKey(g.id));
      return null;
    } catch (err) {
      return err instanceof ApiError ? err.message : "Could not create the group";
    }
  }

  const sel = selected ? current(selected) : null;
  const selRole = sel?.kind === "role" ? data.roles.find((r) => r.name === sel.name) : undefined;
  const selGroup = sel?.kind === "group" ? data.groups.find((g) => g.id === sel.id) : undefined;
  const roleEditable = sel?.kind === "role" ? (sel.isNew ? data.can_manage_roles : Boolean(selRole?.editable)) : false;

  return (
    <div className="iam-section" aria-labelledby="iam-access-heading">
      <h2 id="iam-access-heading" className="visually-hidden">
        Roles and groups
      </h2>
      {error ? <ErrorNotice error={error} /> : null}
      {dirty ? (
        <div className="iam-savebar" role="status">
          <span>
            {dirty} unsaved change{dirty === 1 ? "" : "s"}
          </span>
          <button className="btn btn-sm btn-ghost" type="button" disabled={saving} onClick={() => setDrafts({})}>
            Discard
          </button>
          <button className="btn btn-sm btn-primary" type="button" disabled={saving} onClick={() => void saveAll()}>
            {saving ? "Saving" : "Save all changes"}
          </button>
        </div>
      ) : null}
      <div className="iam-split">
        <nav className="iam-list" aria-label="Roles and groups">
          <div className="iam-list-head">
            <span>Roles · highest first</span>
            <span className="btn-row is-flush">
              {order ? (
                <button className="btn btn-sm btn-ghost" type="button" disabled={saving} onClick={() => setOrder(null)}>
                  Reset
                </button>
              ) : null}
              {data.can_order ? (
                <button className="btn btn-sm btn-primary" type="button" disabled={!order || saving} title="Save the role order" onClick={() => void saveOrder()}>
                  Save
                </button>
              ) : null}
              <button className="btn btn-sm btn-secondary btn-icon" type="button" aria-label="Create a role or group" title="Create a role or group" onClick={() => setCreating(true)}>
                <Icon name="create" size={12} />
              </button>
            </span>
          </div>
          {data.can_order ? <p className="iam-list-hint">Drag to reorder, or Alt+Up/Down. A role cannot act on any role above it.</p> : null}
          {roles.map((r, i) => {
            const key = roleKey(r.name);
            const canMove = movable(i);
            return (
              <button
                key={key}
                type="button"
                draggable={canMove}
                className={"iam-list-item" + (selected === key ? " is-active" : "") + (drag === r.name ? " is-dragging" : "") + (canMove ? " is-movable" : "")}
                aria-current={selected === key ? "true" : undefined}
                aria-keyshortcuts={canMove ? "Alt+ArrowUp Alt+ArrowDown" : undefined}
                onClick={() => setSelected(key)}
                onDragStart={(e) => {
                  e.dataTransfer.effectAllowed = "move";
                  setDrag(r.name);
                }}
                onDragOver={(e) => {
                  if (drag && canMove) {
                    e.preventDefault();
                    if (drag !== r.name) {
                      moveRole(drag, i);
                    }
                  }
                }}
                onDragEnd={() => setDrag(null)}
                onKeyDown={(e) => {
                  if (canMove && e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
                    e.preventDefault();
                    moveRole(r.name, e.key === "ArrowUp" ? i - 1 : i + 1);
                  }
                }}
              >
                <span className="iam-rank">{i + 1}</span>
                <span className="iam-list-label">{r.title}</span>
                {drafts[key] ? <span className="iam-dot" aria-label="Unsaved changes" /> : null}
                {!r.editable || !canMove ? <Icon name="lock" size={12} /> : null}
                <span className="meta">{r.user_count} user(s)</span>
              </button>
            );
          })}
          {newRoles.map((d) => {
            const key = roleKey(d.name);
            return (
              <button key={key} type="button" className={"iam-list-item" + (selected === key ? " is-active" : "")} onClick={() => setSelected(key)}>
                <span className="iam-rank">+</span>
                <span className="iam-list-label">{d.title || d.name}</span>
                <span className="iam-dot" aria-label="Unsaved changes" />
                <span className="meta">new</span>
              </button>
            );
          })}
          <p className="iam-list-head">Groups</p>
          {groups.length === 0 ? <p className="muted iam-list-empty">No groups yet.</p> : null}
          {groups.map((g) => {
            const key = groupKey(g.id);
            return (
              <button key={key} type="button" className={"iam-list-item" + (selected === key ? " is-active" : "")} onClick={() => setSelected(key)}>
                <Icon name="users" size={12} />
                <span className="iam-list-label">{g.name}</span>
                {drafts[key] ? <span className="iam-dot" aria-label="Unsaved changes" /> : null}
                {!g.editable ? <Icon name="lock" size={12} /> : null}
                <span className="meta">{g.member_ids.length} member(s)</span>
              </button>
            );
          })}
        </nav>
        <div className="iam-editor">
          {!sel ? (
            <p className="muted">Pick a role or group.</p>
          ) : sel.kind === "role" ? (
            <RoleEditor
              draft={sel}
              role={selRole}
              editable={roleEditable}
              all={data.permissions}
              roles={data.roles}
              onChange={(d) => update(roleKey(d.name), d)}
              onDelete={async (reassign) => {
                try {
                  if (sel.isNew) {
                    setDrafts((cur) => {
                      const out = { ...cur };
                      delete out[roleKey(sel.name)];
                      return out;
                    });
                  } else {
                    await deleteIAMRole(sel.name, reassign);
                    await load();
                  }
                  setSelected(null);
                } catch (err) {
                  setError(err instanceof ApiError ? err.message : "Delete failed");
                }
              }}
            />
          ) : (
            <GroupEditor
              draft={sel}
              editable={Boolean(selGroup?.editable)}
              roles={data.roles}
              users={data.users}
              onChange={(d) => update(groupKey(d.id), d)}
              onDelete={async () => {
                try {
                  await deleteIAMGroup(sel.id);
                  setSelected(null);
                  await load();
                } catch (err) {
                  setError(err instanceof ApiError ? err.message : "Delete failed");
                }
              }}
            />
          )}
        </div>
      </div>
      <AutomationCreatorsPanel roles={data.roles} />
      {creating ? <CreateDialog canRole={data.can_manage_roles} onClose={() => setCreating(false)} onRole={newRole} onGroup={newGroup} /> : null}
    </div>
  );
}

function RoleEditor({
  draft,
  role,
  editable,
  all,
  roles,
  onChange,
  onDelete,
}: {
  draft: RoleDraft;
  role?: IAMRole;
  editable: boolean;
  all: string[];
  roles: IAMRole[];
  onChange: (d: RoleDraft) => void;
  onDelete: (reassign?: string) => Promise<void>;
}) {
  const [filter, setFilter] = useState("");
  const [reassign, setReassign] = useState("");
  const owner = Boolean(role?.owner);
  const perms = new Set(draft.permissions);
  const f = filter.trim().toLowerCase();
  const categories = useMemo(() => {
    const map = new Map<string, string[]>();
    for (const p of all) {
      const c = categoryOf(p);
      if (f && !p.includes(f) && !permissionLabel(p).toLowerCase().includes(f) && !c.toLowerCase().includes(f)) {
        continue;
      }
      map.set(c, [...(map.get(c) ?? []), p]);
    }
    return [...map.entries()];
  }, [all, f]);
  const set = (next: Partial<RoleDraft>) => onChange({ ...draft, ...next });
  const toggle = (p: string, on: boolean) => set({ permissions: on ? [...draft.permissions, p] : draft.permissions.filter((x) => x !== p) });

  return (
    <div className="stack">
      <div className="iam-editor-head">
        <div>
          <h3 className="iam-editor-title">{draft.title || draft.name}</h3>
          <p className="meta">
            {draft.isNew ? "New role" : role?.builtin ? "Built-in role" : "Custom role"} · id <code>{draft.name}</code>
            {role ? ` · ${role.user_count} user(s)` : ""}
          </p>
        </div>
        {editable && !owner ? (
          <div className="btn-row is-flush">
            {!draft.isNew ? (
              <select className="field-input" aria-label="Move its users to" value={reassign} onChange={(e) => setReassign(e.target.value)}>
                <option value="">Users keep no role</option>
                {roles
                  .filter((r) => r.name !== draft.name && r.editable)
                  .map((r) => (
                    <option key={r.name} value={r.name}>
                      Move users to {r.title}
                    </option>
                  ))}
              </select>
            ) : null}
            <button
              className="btn btn-sm btn-danger"
              type="button"
              onClick={() => {
                if (draft.isNew || confirmAction(`Delete the ${draft.title} role? ${role?.user_count ?? 0} user(s) and any groups lose it.`)) {
                  void onDelete(reassign || undefined);
                }
              }}
            >
              Delete role
            </button>
          </div>
        ) : null}
      </div>
      {owner ? <p className="banner">The SuperAdmin role has every permission. Only the root SuperAdmin can rank roles above it.</p> : null}
      {role?.above_top ? <p className="banner">This role is ranked above SuperAdmin. Only the root SuperAdmin manages it, and the root account holds its permissions.</p> : null}
      {!editable && !owner ? <p className="banner">This role is at or above yours in the hierarchy, so you can view it but not change it.</p> : null}
      <div className="iam-fields">
        <label className="field">
          <span className="field-label">Name</span>
          <input className="field-input" value={draft.title} disabled={!editable || !draft.isNew} onChange={(e) => set({ title: e.target.value })} />
          <span className="field-hint">
            Id <code>{draft.isNew ? roleIDFor(draft.title) || draft.name : draft.name}</code>. The id follows the name, so renaming a role means creating a new one.
          </span>
        </label>
        <label className="field iam-field-wide">
          <span className="field-label">Description</span>
          <input className="field-input" value={draft.summary} disabled={!editable} onChange={(e) => set({ summary: e.target.value })} />
        </label>
        <label className="check-row">
          <input type="checkbox" checked={draft.login} disabled={!editable} onChange={(e) => set({ login: e.target.checked })} />
          <span>People can sign in with this role</span>
        </label>
      </div>
      {!owner ? (
        <section className="iam-perms" aria-label="Permissions">
          <div className="iam-perms-head">
            <h4>
              Permissions <span className="meta">{draft.permissions.length} of {all.length}</span>
            </h4>
            <input className="field-input" type="search" aria-label="Filter permissions" placeholder="Filter permissions" value={filter} onChange={(e) => setFilter(e.target.value)} />
          </div>
          <div className="iam-perm-grid">
            {categories.map(([cat, items]) => {
              const on = items.filter((p) => perms.has(p)).length;
              return (
                <fieldset key={cat} className="iam-perm-group">
                  <legend>
                    <label className="check-row">
                      <input
                        type="checkbox"
                        disabled={!editable}
                        checked={on === items.length}
                        ref={(el) => {
                          if (el) {
                            el.indeterminate = on > 0 && on < items.length;
                          }
                        }}
                        onChange={(e) =>
                          set({
                            permissions: e.target.checked
                              ? [...new Set([...draft.permissions, ...items])]
                              : draft.permissions.filter((p) => !items.includes(p)),
                          })
                        }
                      />
                      <span>{cat}</span>
                    </label>
                  </legend>
                  {items.map((p) => (
                    <label key={p} className="check-row iam-perm">
                      <input type="checkbox" disabled={!editable} checked={perms.has(p)} onChange={(e) => toggle(p, e.target.checked)} />
                      <span title={p}>{permissionLabel(p)}</span>
                    </label>
                  ))}
                </fieldset>
              );
            })}
          </div>
        </section>
      ) : null}
    </div>
  );
}

function GroupEditor({
  draft,
  editable,
  roles,
  users,
  onChange,
  onDelete,
}: {
  draft: GroupDraft;
  editable: boolean;
  roles: IAMRole[];
  users: IAMAccess["users"];
  onChange: (d: GroupDraft) => void;
  onDelete: () => Promise<void>;
}) {
  const [filter, setFilter] = useState("");
  const set = (next: Partial<GroupDraft>) => onChange({ ...draft, ...next });
  const f = filter.trim().toLowerCase();
  return (
    <div className="stack">
      <div className="iam-editor-head">
        <div>
          <h3 className="iam-editor-title">{draft.name}</h3>
          <p className="meta">
            Group · {draft.members.length} member(s) · {draft.roles.length} role(s)
          </p>
        </div>
        {editable ? (
          <button
            className="btn btn-sm btn-danger"
            type="button"
            onClick={() => {
              if (confirmAction(`Delete the group ${draft.name}? Its members keep their own roles.`)) {
                void onDelete();
              }
            }}
          >
            Delete group
          </button>
        ) : null}
      </div>
      {!editable ? <p className="banner">This group holds a role at or above yours, so you can view it but not change it.</p> : null}
      <label className="field">
        <span className="field-label">Name</span>
        <input className="field-input" value={draft.name} disabled={!editable} onChange={(e) => set({ name: e.target.value })} />
      </label>
      <section className="iam-perms" aria-label="Roles">
        <h4>Roles members get</h4>
        <div className="iam-chip-list">
          {roles
            .filter((r) => !r.owner || draft.roles.includes(r.name))
            .map((r) => (
              <label key={r.name} className="check-row iam-chip">
                <input
                  type="checkbox"
                  disabled={!editable || !r.editable}
                  checked={draft.roles.includes(r.name)}
                  onChange={(e) => set({ roles: e.target.checked ? [...draft.roles, r.name] : draft.roles.filter((x) => x !== r.name) })}
                />
                <span>{r.title}</span>
              </label>
            ))}
        </div>
      </section>
      <section className="iam-perms" aria-label="Members">
        <div className="iam-perms-head">
          <h4>Members</h4>
          <input className="field-input" type="search" aria-label="Filter people" placeholder="Filter people" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </div>
        <div className="iam-chip-list">
          {users
            .filter((u) => !f || `${u.username} ${u.display_name ?? ""}`.toLowerCase().includes(f))
            .map((u) => (
              <label key={u.id} className="check-row iam-chip">
                <input
                  type="checkbox"
                  disabled={!editable}
                  checked={draft.members.includes(u.id)}
                  onChange={(e) => set({ members: e.target.checked ? [...draft.members, u.id] : draft.members.filter((x) => x !== u.id) })}
                />
                <span>{u.display_name || u.username}</span>
              </label>
            ))}
        </div>
      </section>
    </div>
  );
}

/** The id a role named title gets; the server and migration 0076 use the same rule. */
export function roleIDFor(title: string): string {
  return title
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
}

/** CreateDialog is the one + button: pick Role or Group, and the form below follows. */
function CreateDialog({
  canRole,
  onClose,
  onRole,
  onGroup,
}: {
  canRole: boolean;
  onClose: () => void;
  /** Adds a role draft; returns an error message or null. */
  onRole: (title: string, summary: string) => string | null;
  onGroup: (name: string) => Promise<string | null>;
}) {
  const [kind, setKind] = useState<"role" | "group">(canRole ? "role" : "group");
  const [name, setName] = useState("");
  const [summary, setSummary] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const id = roleIDFor(name);
  return (
    <Dialog open title={kind === "role" ? "New role" : "New group"} onClose={onClose}>
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          const title = name.trim();
          if (!title) {
            setError("A name is required");
            return;
          }
          if (kind === "role") {
            const err = onRole(title, summary.trim());
            if (err) {
              setError(err);
              return;
            }
            onClose();
            return;
          }
          setBusy(true);
          void onGroup(title).then((err) => {
            setBusy(false);
            if (err) {
              setError(err);
              return;
            }
            onClose();
          });
        }}
      >
        <label className="field">
          <span className="field-label">Type</span>
          <select className="field-input" value={kind} onChange={(e) => setKind(e.target.value as "role" | "group")}>
            {canRole ? <option value="role">Role</option> : null}
            <option value="group">Group</option>
          </select>
        </label>
        <label className="field">
          <span className="field-label">Name</span>
          <input className="field-input" value={name} autoFocus onChange={(e) => setName(e.target.value)} placeholder={kind === "role" ? "Support" : "Operators"} />
          {kind === "role" ? (
            <span className="field-hint">
              Id <code>{id || "…"}</code>. Pick its permissions after creating it, then save.
            </span>
          ) : (
            <span className="field-hint">Add roles and members after creating it.</span>
          )}
        </label>
        {kind === "role" ? (
          <label className="field">
            <span className="field-label">Description</span>
            <input className="field-input" value={summary} onChange={(e) => setSummary(e.target.value)} />
          </label>
        ) : null}
        {error ? <ErrorNotice error={error} /> : null}
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {kind === "role" ? "Add role" : busy ? "Creating" : "Create group"}
          </button>
        </div>
      </form>
    </Dialog>
  );
}
