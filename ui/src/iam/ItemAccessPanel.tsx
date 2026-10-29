import { useEffect, useMemo, useState } from "react";
import {
  deleteResourceAccess,
  getDocker,
  getIAMAccess,
  listGroups,
  listManagedUsers,
  listNodes,
  listResourceAccess,
  listWorkloads,
  putResourceAccess,
  type GrantScope,
  type ItemKind,
  type ResourceAccess,
  type ResourceGrant,
} from "../api/client";
import { listGameServers } from "../gameservers/api";
import { ActionMenu } from "../components/ActionMenu";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EmptyState, LoadingState } from "../components/EmptyState";
import { ErrorNotice } from "../components/ErrorNotice";
import { Icon } from "../components/Icon";
import { PageHeader, SectionScopeProvider } from "../components/PageHeader";
import { ResourceTable } from "../components/ResourceTable";
import { SelectField } from "../components/form/SelectField";
import { permissionLabel, roleLabel } from "../labels";
import { useNodeNicknames } from "../nodeNames";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";
import {
  KIND_LABEL,
  KIND_ORDER,
  PRESET_LABEL,
  READ_PERMISSION,
  SUPERADMIN_ONLY,
  accessSummary,
  dockerItemID,
  matchingPreset,
  permissionSections,
  presetPermissions,
  scopeLabel,
  scopePermissionLabel,
  withRead,
  type PresetId,
} from "./itemAccess";

type Option = { id: string; label: string };
type SubjectKind = "user" | "group" | "role";

type Lists = {
  users: Option[];
  groups: Option[];
  roles: Option[];
  items: Record<ItemKind, Option[]>;
};

const EMPTY_LISTS: Lists = { users: [], groups: [], roles: [], items: { workload: [], gameserver: [], docker: [], node: [] } };

const SUBJECT_LABEL: Record<SubjectKind, string> = { user: "User", group: "Group", role: "Role" };

type Draft = {
  id?: string;
  subjectKind: SubjectKind;
  subjectID: string;
  scope: GrantScope;
  /** The kind of item last chosen, kept while "Everywhere" is picked. */
  kind: ItemKind;
  itemID: string;
  perms: string[];
};

function settled<T>(r: PromiseSettledResult<T>): T | null {
  return r.status === "fulfilled" ? r.value : null;
}

function byLabel(a: Option, b: Option): number {
  return a.label.localeCompare(b.label);
}

function messageOf(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}

function suggestedFor(access: ResourceAccess | null, scope: GrantScope): string[] {
  if (!access || scope === "cluster") {
    return [];
  }
  return access.suggested?.[scope] ?? [];
}

/**
 * ItemAccessPanel lists direct permissions: grants to a user, group or role,
 * everywhere or on one workload, game server, Docker container or node, on
 * top of what their roles give. The server checks who may give what.
 */
export function ItemAccessPanel({ superAdmin = false }: { superAdmin?: boolean }) {
  const nodeLabel = useNodeNicknames();
  const [access, setAccess] = useState<ResourceAccess | null>(null);
  const [lists, setLists] = useState<Lists>(EMPTY_LISTS);
  const [error, setError] = useState<unknown>(null);
  const [filter, setFilter] = useState("");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [removing, setRemoving] = useState<ResourceGrant | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([
      listResourceAccess(),
      listManagedUsers({ perPage: 200 }),
      listGroups(),
      getIAMAccess(),
      listWorkloads(),
      listGameServers(),
      getDocker(),
      listNodes(),
    ]).then(([acc, users, groups, iam, workloads, games, docker, nodes]) => {
      if (cancelled) {
        return;
      }
      if (acc.status === "fulfilled") {
        setAccess(acc.value);
      } else {
        setError(acc.reason);
      }
      const containers = new Map<string, Option>();
      for (const c of settled(docker)?.containers ?? []) {
        const id = dockerItemID(c.machine_id, c.name);
        containers.set(id, { id, label: `${c.name.replace(/^\//, "")} on ${nodeLabel(c.machine_id, c.machine_name)}` });
      }
      const roles = settled(iam)?.roles ?? [];
      setLists({
        users: (settled(users)?.items ?? [])
          .map((u) => ({ id: u.id, label: u.display_name ? `${u.display_name} (${u.username})` : u.username }))
          .sort(byLabel),
        groups: (settled(groups)?.items ?? []).map((g) => ({ id: g.id, label: g.name })).sort(byLabel),
        // Roles keep the server's order: highest rank first.
        roles: roles.length
          ? roles.map((r) => ({ id: r.name, label: r.title || roleLabel(r.name) }))
          : ["admin", "user", "automation"].map((id) => ({ id, label: roleLabel(id) })),
        items: {
          workload: (settled(workloads)?.items ?? []).map((w) => ({ id: w.id, label: w.name || w.id })).sort(byLabel),
          gameserver: (settled(games)?.items ?? []).map((g) => ({ id: g.id, label: g.name || g.id })).sort(byLabel),
          docker: [...containers.values()].sort(byLabel),
          node: (settled(nodes) ?? []).map((n) => ({ id: n.id, label: nodeLabel(n.id, n.name) || n.id })).sort(byLabel),
        },
      });
    });
    return () => {
      cancelled = true;
    };
  }, [nodeLabel]);

  function subjectName(g: ResourceGrant): string {
    const list = g.subject_kind === "group" ? lists.groups : g.subject_kind === "role" ? lists.roles : lists.users;
    const found = list.find((o) => o.id === g.subject_id)?.label;
    if (found) {
      return found;
    }
    return g.subject_kind === "role" ? roleLabel(g.subject_id) : (g.subject_name ?? g.subject_id);
  }

  function itemName(kind: ItemKind, id: string): string | null {
    return lists.items[kind]?.find((o) => o.id === id)?.label ?? null;
  }

  function whereText(g: ResourceGrant): string {
    if (g.resource_kind === "cluster") {
      return "everywhere";
    }
    return `on ${itemName(g.resource_kind, g.resource_id) ?? g.resource_id}`;
  }

  const q = filter.trim().toLowerCase();
  const rows = (access?.grants ?? [])
    .map((g) => {
      const scope = g.resource_kind;
      const item = scope === "cluster" ? null : itemName(scope, g.resource_id);
      return {
        grant: g,
        who: subjectName(g),
        item,
        summary: accessSummary(scope, g.permissions, suggestedFor(access, scope)),
      };
    })
    .filter((r) => {
      if (!q) {
        return true;
      }
      const hay = [
        r.who,
        SUBJECT_LABEL[r.grant.subject_kind],
        r.item ?? r.grant.resource_id,
        scopeLabel(r.grant.resource_kind),
        r.summary,
        ...r.grant.permissions.map((p) => permissionLabel(p)),
      ]
        .join(" ")
        .toLowerCase();
      return hay.includes(q);
    })
    .sort((a, b) => a.who.localeCompare(b.who) || (a.item ?? "").localeCompare(b.item ?? ""));

  function openNew() {
    const kind: ItemKind = "workload";
    setDialogError(null);
    setDraft({
      subjectKind: "user",
      subjectID: "",
      scope: kind,
      kind,
      itemID: "",
      perms: presetPermissions(kind, "view", suggestedFor(access, kind)),
    });
  }

  function openEdit(g: ResourceGrant) {
    const scope = g.resource_kind;
    setDialogError(null);
    setDraft({
      id: g.id,
      subjectKind: g.subject_kind,
      subjectID: g.subject_id,
      scope,
      kind: scope === "cluster" ? "workload" : scope,
      itemID: g.resource_id,
      perms: scope === "cluster" ? [...g.permissions] : withRead(scope, g.permissions),
    });
  }

  async function save() {
    if (!draft || !draft.subjectID || (draft.scope !== "cluster" && !draft.itemID) || draft.perms.length === 0) {
      return;
    }
    setBusy(true);
    setDialogError(null);
    try {
      const next = await putResourceAccess({
        ...(draft.id ? { id: draft.id } : {}),
        subject_kind: draft.subjectKind,
        subject_id: draft.subjectID,
        resource_kind: draft.scope,
        resource_id: draft.scope === "cluster" ? "" : draft.itemID,
        permissions: draft.perms,
      });
      setAccess(next);
      setDraft(null);
    } catch (err) {
      setDialogError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!removing) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      setAccess(await deleteResourceAccess(removing.id));
      setRemoving(null);
    } catch (err) {
      setError(err);
      setRemoving(null);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section id="item-access" className="iam-section item-access" aria-labelledby="item-access-heading">
      <SectionScopeProvider>
        <PageHeader
          id="item-access-heading"
          title="Direct permissions"
          kicker="Permissions given straight to a user, group or role, on top of what their roles give. Each grant applies everywhere or to one workload, game server, Docker container or node."
          actions={
            <button className="btn btn-primary btn-sm" type="button" disabled={!access} onClick={openNew}>
              Give permissions
            </button>
          }
        />
      </SectionScopeProvider>
      {error ? <ErrorNotice error={error} /> : null}
      {!access && !error ? <LoadingState label="Loading direct permissions" /> : null}
      {access ? (
        <>
          {access.grants.length > 0 ? (
            <div className="toolbar">
              <label className="search-field">
                <Icon name="search" size={14} />
                <input
                  className="field-input"
                  type="search"
                  placeholder="Filter by who, where or permission"
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  aria-label="Filter direct permissions"
                />
              </label>
            </div>
          ) : null}
          <ResourceTable
            className="item-access-table"
            headers={["Who", "Where", "Permissions", ""]}
            empty={
              <EmptyState title={access.grants.length ? "No grants match" : "No direct permissions yet"}>
                {access.grants.length
                  ? "Try a different filter."
                  : "Give a user, group or role extra permissions, everywhere or on one workload, game server, Docker container or node."}
              </EmptyState>
            }
            rows={rows.map((r) => {
              const scope = r.grant.resource_kind;
              return [
                <span key="who">
                  {r.who}
                  {r.grant.subject_kind !== "user" ? <span className="tag-inline">{SUBJECT_LABEL[r.grant.subject_kind]}</span> : null}
                </span>,
                <span key="where">
                  {scope === "cluster" ? (
                    "Everywhere"
                  ) : (
                    <>
                      <span className="item-access-kind">{KIND_LABEL[scope] ?? scope}</span>{" "}
                      {r.item ?? (
                        <span className="item-access-missing">
                          <code>{r.grant.resource_id}</code> (missing)
                        </span>
                      )}
                    </>
                  )}
                </span>,
                <span key="perms" title={r.grant.permissions.map((p) => scopePermissionLabel(scope, p, suggestedFor(access, scope))).join(", ")}>
                  {r.summary}
                </span>,
                <ActionMenu
                  key="act"
                  label={`Actions for ${r.who} ${whereText(r.grant)}`}
                  items={[
                    { label: "Edit", onClick: () => openEdit(r.grant) },
                    { label: "Remove", danger: true, onClick: () => setRemoving(r.grant) },
                  ]}
                />,
              ];
            })}
          />
        </>
      ) : null}
      {draft && access ? (
        <GrantDialog
          draft={draft}
          lists={lists}
          access={access}
          superAdmin={superAdmin}
          busy={busy}
          error={dialogError}
          onChange={setDraft}
          onClose={() => setDraft(null)}
          onSave={() => void save()}
        />
      ) : null}
      <ConfirmDialog
        open={Boolean(removing)}
        title="Remove direct permissions"
        danger
        confirmLabel="Remove"
        onClose={() => setRemoving(null)}
        onConfirm={() => void remove()}
      >
        {removing ? (
          <p>
            {subjectName(removing)} loses these direct permissions {whereText(removing)}. Permissions from roles and groups are not
            changed.
          </p>
        ) : null}
      </ConfirmDialog>
    </section>
  );
}

function GrantDialog({
  draft,
  lists,
  access,
  superAdmin,
  busy,
  error,
  onChange,
  onClose,
  onSave,
}: {
  draft: Draft;
  lists: Lists;
  access: ResourceAccess;
  superAdmin: boolean;
  busy: boolean;
  error: string | null;
  onChange: (d: Draft) => void;
  onClose: () => void;
  onSave: () => void;
}) {
  const [search, setSearch] = useState("");
  const editing = Boolean(draft.id);
  const isItem = draft.scope !== "cluster";
  const suggested = suggestedFor(access, draft.scope);
  const read = draft.scope === "cluster" ? null : READ_PERMISSION[draft.scope];
  const subjects = draft.subjectKind === "group" ? lists.groups : draft.subjectKind === "role" ? lists.roles : lists.users;
  const items = lists.items[draft.kind];
  const preset = draft.scope === "cluster" ? null : matchingPreset(draft.scope, draft.perms, suggested);
  // Only a SuperAdmin gives "*" or the override; keep them visible on a grant that already has them.
  const all = useMemo(
    () => (access.permissions ?? []).filter((p) => superAdmin || !SUPERADMIN_ONLY.includes(p) || draft.perms.includes(p)),
    [access.permissions, superAdmin, draft.perms],
  );
  const sections = useMemo(() => permissionSections(draft.scope, all, suggested), [draft.scope, all, suggested]);
  const ordered = useMemo(() => sections.flatMap((s) => s.perms), [sections]);
  // Keep an item that no longer exists choosable while editing its grant.
  const itemOptions = draft.itemID && !items.some((o) => o.id === draft.itemID) ? [{ id: draft.itemID, label: `${draft.itemID} (missing)` }, ...items] : items;
  const subjectOptions =
    draft.subjectID && !subjects.some((o) => o.id === draft.subjectID)
      ? [{ id: draft.subjectID, label: draft.subjectKind === "role" ? roleLabel(draft.subjectID) : draft.subjectID }, ...subjects]
      : subjects;

  const f = search.trim().toLowerCase();
  const shown = sections
    .map((s) => ({
      group: s.group,
      perms: f
        ? s.perms.filter(
            (p) =>
              p.toLowerCase().includes(f) ||
              scopePermissionLabel(draft.scope, p, suggested).toLowerCase().includes(f) ||
              permissionLabel(p).toLowerCase().includes(f) ||
              s.group.toLowerCase().includes(f),
          )
        : s.perms,
    }))
    .filter((s) => s.perms.length > 0);

  function setPerms(next: string[]) {
    const keep = new Set(next);
    if (read) {
      keep.add(read);
    }
    const inOrder = ordered.filter((p) => keep.has(p));
    onChange({ ...draft, perms: [...inOrder, ...[...keep].filter((p) => !ordered.includes(p))] });
  }

  function toggle(p: string, on: boolean) {
    setPerms(on ? [...draft.perms, p] : draft.perms.filter((x) => x !== p));
  }

  function setWhere(where: "cluster" | "item") {
    if (where === "cluster") {
      onChange({ ...draft, scope: "cluster", itemID: "", perms: [] });
      return;
    }
    const kind = draft.kind;
    onChange({ ...draft, scope: kind, itemID: "", perms: presetPermissions(kind, "view", suggestedFor(access, kind)) });
  }

  const hasAll = draft.perms.includes("*");
  const rootOnNode = draft.scope === "node";
  const hostEverywhere = draft.perms.includes("node.host");
  const canSave = Boolean(draft.subjectID) && (!isItem || Boolean(draft.itemID)) && draft.perms.length > 0;

  return (
    <Dialog
      open
      title={editing ? "Edit direct permissions" : "Give permissions"}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button className="btn btn-primary" type="submit" form="item-access-form" disabled={busy || !canSave}>
            {editing ? "Save" : "Give permissions"}
          </button>
        </div>
      }
    >
      <form
        id="item-access-form"
        className="form item-access-form"
        onSubmit={(e) => {
          e.preventDefault();
          onSave();
        }}
      >
        {error ? (
          <p className="banner banner-error" role="alert">
            {error}
          </p>
        ) : null}
        <div className="field">
          <span className="field-label">Who</span>
          <Segmented<SubjectKind>
            ariaLabel="Give permissions to"
            value={draft.subjectKind}
            options={(["user", "group", "role"] as SubjectKind[]).map((k) => ({ id: k, label: SUBJECT_LABEL[k], disabled: editing }))}
            onChange={(v) => onChange({ ...draft, subjectKind: v, subjectID: "" })}
          />
        </div>
        <SelectField
          id="item-access-subject"
          label={SUBJECT_LABEL[draft.subjectKind]}
          value={draft.subjectID}
          disabled={editing}
          required
          hint={draft.subjectKind === "role" ? "Everyone with this role gets these permissions." : undefined}
          onChange={(e) => onChange({ ...draft, subjectID: e.target.value })}
        >
          <option value="">{`Choose a ${SUBJECT_LABEL[draft.subjectKind].toLowerCase()}`}</option>
          {subjectOptions.map((o) => (
            <option key={o.id} value={o.id}>
              {o.label}
            </option>
          ))}
        </SelectField>
        <div className="field">
          <span className="field-label">Where</span>
          <Segmented<"cluster" | "item">
            ariaLabel="Where"
            value={isItem ? "item" : "cluster"}
            options={[
              { id: "cluster", label: "Everywhere" },
              { id: "item", label: "One item" },
            ]}
            onChange={setWhere}
          />
        </div>
        {isItem ? (
          <>
            <div className="field">
              <span className="field-label">Kind of item</span>
              <Segmented<ItemKind>
                ariaLabel="Kind of item"
                value={draft.kind}
                options={KIND_ORDER.map((k) => ({ id: k, label: KIND_LABEL[k] }))}
                onChange={(k) =>
                  onChange({ ...draft, scope: k, kind: k, itemID: "", perms: presetPermissions(k, "view", suggestedFor(access, k)) })
                }
              />
            </div>
            <SelectField
              id="item-access-item"
              label={KIND_LABEL[draft.kind]}
              value={draft.itemID}
              required
              hint={draft.kind === "docker" ? "Access follows the container name on that machine, so it survives the container being recreated." : undefined}
              onChange={(e) => onChange({ ...draft, itemID: e.target.value })}
            >
              <option value="">{items.length ? `Choose a ${KIND_LABEL[draft.kind].toLowerCase()}` : "None found"}</option>
              {itemOptions.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.label}
                </option>
              ))}
            </SelectField>
          </>
        ) : null}
        {rootOnNode ? (
          <p className="banner banner-warn" role="note">
            Terminal and file access on a node are root access to that machine: everything on it, including other workloads and
            their data.
          </p>
        ) : null}
        {hostEverywhere ? (
          <p className="banner banner-warn" role="note">
            Root terminal and files on node hosts is root access to every node: everything on them, including other workloads and
            their data.
          </p>
        ) : null}
        {hasAll ? (
          <p className="banner banner-warn" role="note">
            Everything is full SuperAdmin power{isItem ? " over this item" : ""}: every permission, including ones added later.
          </p>
        ) : null}
        {draft.perms.includes("permissions.assign") ? (
          <p className="banner banner-warn" role="note">
            Give permissions lets them hand out the permissions they hold to users, groups and roles ranked below them.
          </p>
        ) : null}
        <div className="field">
          <span className="field-label">
            Permissions <span className="meta">{draft.perms.length} chosen</span>
          </span>
          {draft.scope !== "cluster" ? (
            <div className="btn-row is-flush" role="group" aria-label="Presets">
              {(["view", "operate", "full"] as PresetId[]).map((id) => (
                <button
                  key={id}
                  type="button"
                  className={"btn btn-sm " + (preset === id ? "btn-primary" : "btn-ghost")}
                  aria-pressed={preset === id}
                  onClick={() => draft.scope !== "cluster" && setPerms(presetPermissions(draft.scope, id, suggested))}
                >
                  {PRESET_LABEL[id]}
                </button>
              ))}
            </div>
          ) : null}
          <input
            className="field-input"
            type="search"
            aria-label="Filter permissions"
            placeholder="Filter permissions"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <div className="item-access-perm-list">
            {shown.length === 0 ? <p className="field-hint">No permissions match.</p> : null}
            {shown.map(({ group, perms }) => (
              <fieldset key={group} className="item-access-perms">
                <legend>{group}</legend>
                {perms.map((p) => (
                  <label key={p} className="check" title={p}>
                    <input
                      type="checkbox"
                      checked={p === read || draft.perms.includes(p)}
                      disabled={p === read || (!superAdmin && SUPERADMIN_ONLY.includes(p))}
                      onChange={(e) => toggle(p, e.target.checked)}
                    />
                    <span>
                      {scopePermissionLabel(draft.scope, p, suggested)}
                      {p === read ? <span className="field-hint"> (always included)</span> : null}
                    </span>
                  </label>
                ))}
              </fieldset>
            ))}
          </div>
        </div>
        <p className="field-hint">
          A SuperAdmin can give anything. Holders of &ldquo;Give permissions without rank limits&rdquo; can give any permission except
          Everything and that one. Everyone else needs Admin rank or above, and can give only permissions they hold, to users, groups
          and roles ranked below them.
        </p>
      </form>
    </Dialog>
  );
}
