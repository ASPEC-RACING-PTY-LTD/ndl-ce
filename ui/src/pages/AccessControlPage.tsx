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
import { Dialog } from "../ui/Dialog";

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
  return perm.split(".")[0];
}

export function AccessControlPage() {
  const [data, setData] = useState<IAMAccess | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [query, setQuery] = useState("");
  const [saving, setSaving] = useState(false);
  const [ordering, setOrdering] = useState(false);

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

  const q = query.trim().toLowerCase();
  const roles = data.roles.filter((r) => !q || `${r.title} ${r.name}`.toLowerCase().includes(q));
  const groups = data.groups.filter((g) => !q || g.name.toLowerCase().includes(q));
  const newRoles = Object.values(drafts).filter((d): d is RoleDraft => d.kind === "role" && d.isNew);

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
          await saveIAMRole(d.name, { title: d.title, summary: d.summary, permissions: d.permissions, login: d.login });
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

  function newRole() {
    const raw = window.prompt("New role id (lowercase letters, digits, dashes)", "support");
    const name = raw?.trim().toLowerCase();
    if (!name) {
      return;
    }
    const key = roleKey(name);
    setDrafts((cur) => ({ ...cur, [key]: { kind: "role", isNew: true, name, title: name, summary: "", login: true, permissions: [] } }));
    setSelected(key);
  }

  async function newGroup() {
    const name = window.prompt("New group name", "")?.trim();
    if (!name) {
      return;
    }
    try {
      const g = await createGroup(name);
      await load();
      setSelected(groupKey(g.id));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not create the group");
    }
  }

  const sel = selected ? current(selected) : null;
  const selRole = sel?.kind === "role" ? data.roles.find((r) => r.name === sel.name) : undefined;
  const selGroup = sel?.kind === "group" ? data.groups.find((g) => g.id === sel.id) : undefined;
  const roleEditable = sel?.kind === "role" ? (sel.isNew ? data.can_manage_roles : Boolean(selRole?.editable)) : false;

  return (
    <div className="iam-section" aria-labelledby="iam-access-heading">
      <div className="iam-toolbar">
        <h2 id="iam-access-heading" className="iam-section-title">
          Access control
        </h2>
        <input className="field-input iam-search" type="search" aria-label="Search roles and groups" placeholder="Search roles and groups" value={query} onChange={(e) => setQuery(e.target.value)} />
        <div className="btn-row is-flush iam-toolbar-actions">
          {data.can_order ? (
            <button className="btn btn-sm btn-secondary" type="button" onClick={() => setOrdering(true)}>
              Role hierarchy
            </button>
          ) : null}
          {data.can_manage_roles ? (
            <button className="btn btn-sm btn-secondary" type="button" onClick={newRole}>
              New role
            </button>
          ) : null}
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => void newGroup()}>
            New group
          </button>
        </div>
      </div>
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
          <p className="iam-list-head">Roles · highest first</p>
          {[...roles.map((r) => ({ key: roleKey(r.name), label: r.title, meta: `${r.user_count} user(s)`, locked: !r.editable, rank: r.rank })),
            ...newRoles.map((d) => ({ key: roleKey(d.name), label: d.title || d.name, meta: "new", locked: false, rank: 999 }))].map((it, i) => (
            <button
              key={it.key}
              type="button"
              className={"iam-list-item" + (selected === it.key ? " is-active" : "")}
              aria-current={selected === it.key ? "true" : undefined}
              onClick={() => setSelected(it.key)}
            >
              <span className="iam-rank">{it.rank === 999 ? "+" : i + 1}</span>
              <span className="iam-list-label">{it.label}</span>
              {drafts[it.key] ? <span className="iam-dot" aria-label="Unsaved changes" /> : null}
              {it.locked ? <Icon name="lock" size={12} /> : null}
              <span className="meta">{it.meta}</span>
            </button>
          ))}
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
      {ordering ? (
        <HierarchyDialog
          roles={data.roles}
          onClose={() => setOrdering(false)}
          onSave={async (names) => {
            try {
              await setIAMRoleOrder(names);
              setOrdering(false);
              await load();
            } catch (err) {
              setError(err instanceof ApiError ? err.message : "Could not save the order");
            }
          }}
        />
      ) : null}
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
      if (f && !p.includes(f)) {
        continue;
      }
      const c = categoryOf(p);
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
      {owner ? <p className="banner">The Owner role is always at the top of the hierarchy and has every permission.</p> : null}
      {!editable && !owner ? <p className="banner">This role is at or above yours in the hierarchy, so you can view it but not change it.</p> : null}
      <div className="iam-fields">
        <label className="field">
          <span className="field-label">Title</span>
          <input className="field-input" value={draft.title} disabled={!editable} onChange={(e) => set({ title: e.target.value })} />
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
                      <span>{p.slice(cat.length + 1) || p}</span>
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

function HierarchyDialog({ roles, onClose, onSave }: { roles: IAMRole[]; onClose: () => void; onSave: (names: string[]) => Promise<void> }) {
  const [order, setOrder] = useState(() => roles.map((r) => r.name));
  const [drag, setDrag] = useState<string | null>(null);
  const title = (n: string) => roles.find((r) => r.name === n)?.title ?? n;
  function move(name: string, to: number) {
    setOrder((cur) => {
      const next = cur.filter((n) => n !== name);
      next.splice(Math.max(1, Math.min(to, next.length)), 0, name);
      return next;
    });
  }
  return (
    <Dialog
      open
      title="Role hierarchy"
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-ghost" type="button" onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-primary" type="button" onClick={() => void onSave(order)}>
            Save order
          </button>
        </div>
      }
    >
      <p className="field-hint">Top is the highest. A role cannot change, assign or act on any role above it. The Owner always stays first.</p>
      <ol className="iam-order">
        {order.map((n, i) => (
          <li
            key={n}
            className={"iam-order-item" + (drag === n ? " is-dragging" : "")}
            draggable={i > 0}
            onDragStart={() => setDrag(n)}
            onDragOver={(e) => {
              e.preventDefault();
              if (drag && drag !== n && i > 0) {
                move(drag, i);
              }
            }}
            onDragEnd={() => setDrag(null)}
          >
            <span className="iam-rank">{i + 1}</span>
            <span className="iam-list-label">{title(n)}</span>
            {i > 0 ? (
              <span className="btn-row is-flush">
                <button className="btn btn-ghost btn-sm" type="button" aria-label={`Move ${title(n)} up`} disabled={i <= 1} onClick={() => move(n, i - 1)}>
                  ↑
                </button>
                <button className="btn btn-ghost btn-sm" type="button" aria-label={`Move ${title(n)} down`} disabled={i === order.length - 1} onClick={() => move(n, i + 1)}>
                  ↓
                </button>
              </span>
            ) : (
              <Icon name="lock" size={12} />
            )}
          </li>
        ))}
      </ol>
    </Dialog>
  );
}
