import { useEffect, useMemo, useState } from "react";
import {
  deleteResourceAccess,
  getDocker,
  listGroups,
  listManagedUsers,
  listNodes,
  listResourceAccess,
  listWorkloads,
  putResourceAccess,
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
import { useNodeNicknames } from "../nodeNames";
import { Dialog } from "../ui/Dialog";
import { Segmented } from "../ui/Segmented";
import {
  GROUP_ORDER,
  KIND_LABEL,
  KIND_ORDER,
  PRESET_LABEL,
  READ_PERMISSION,
  accessSummary,
  dockerItemID,
  matchingPreset,
  permissionGroup,
  permissionLabel,
  presetPermissions,
  withRead,
  type PresetId,
} from "./itemAccess";

type Option = { id: string; label: string };
type SubjectKind = "user" | "group";

type Lists = {
  users: Option[];
  groups: Option[];
  items: Record<ItemKind, Option[]>;
};

const EMPTY_LISTS: Lists = { users: [], groups: [], items: { workload: [], gameserver: [], docker: [], node: [] } };

type Draft = {
  id?: string;
  subjectKind: SubjectKind;
  subjectID: string;
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

/**
 * ItemAccessPanel lists grants on single items (one workload, game server,
 * Docker container or node) for users and groups, and gives or changes them.
 */
export function ItemAccessPanel() {
  const nodeLabel = useNodeNicknames();
  const [access, setAccess] = useState<ResourceAccess | null>(null);
  const [lists, setLists] = useState<Lists>(EMPTY_LISTS);
  const [error, setError] = useState<unknown>(null);
  const [filter, setFilter] = useState("");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [removing, setRemoving] = useState<ResourceGrant | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([
      listResourceAccess(),
      listManagedUsers({ perPage: 200 }),
      listGroups(),
      listWorkloads(),
      listGameServers(),
      getDocker(),
      listNodes(),
    ]).then(([acc, users, groups, workloads, games, docker, nodes]) => {
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
      setLists({
        users: (settled(users)?.items ?? [])
          .map((u) => ({ id: u.id, label: u.display_name ? `${u.display_name} (${u.username})` : u.username }))
          .sort(byLabel),
        groups: (settled(groups)?.items ?? []).map((g) => ({ id: g.id, label: g.name })).sort(byLabel),
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

  const grantable = useMemo(() => (access?.grantable ?? {}) as Record<ItemKind, string[]>, [access]);

  function subjectName(g: ResourceGrant): string {
    const list = g.subject_kind === "group" ? lists.groups : lists.users;
    return list.find((o) => o.id === g.subject_id)?.label ?? g.subject_name ?? g.subject_id;
  }

  function itemName(kind: ItemKind, id: string): string | null {
    return lists.items[kind]?.find((o) => o.id === id)?.label ?? null;
  }

  const q = filter.trim().toLowerCase();
  const rows = (access?.grants ?? [])
    .map((g) => {
      const kind = g.resource_kind as ItemKind;
      const item = itemName(kind, g.resource_id);
      return {
        grant: g,
        who: subjectName(g),
        item,
        summary: accessSummary(kind, g.permissions, grantable[kind] ?? []),
      };
    })
    .filter((r) => {
      if (!q) {
        return true;
      }
      const hay = [r.who, r.item ?? r.grant.resource_id, KIND_LABEL[r.grant.resource_kind as ItemKind], r.summary].join(" ").toLowerCase();
      return hay.includes(q);
    })
    .sort((a, b) => a.who.localeCompare(b.who) || (a.item ?? "").localeCompare(b.item ?? ""));

  function openNew() {
    const kind: ItemKind = "workload";
    setDraft({
      subjectKind: "user",
      subjectID: "",
      kind,
      itemID: "",
      perms: presetPermissions(kind, "view", grantable[kind] ?? []),
    });
  }

  function openEdit(g: ResourceGrant) {
    const kind = g.resource_kind as ItemKind;
    setDraft({
      id: g.id,
      subjectKind: g.subject_kind,
      subjectID: g.subject_id,
      kind,
      itemID: g.resource_id,
      perms: withRead(kind, g.permissions),
    });
  }

  async function save() {
    if (!draft || !draft.subjectID || !draft.itemID) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const next = await putResourceAccess({
        ...(draft.id ? { id: draft.id } : {}),
        subject_kind: draft.subjectKind,
        subject_id: draft.subjectID,
        resource_kind: draft.kind,
        resource_id: draft.itemID,
        permissions: draft.perms,
      });
      setAccess(next);
      setDraft(null);
    } catch (err) {
      setError(err);
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
    } finally {
      setBusy(false);
    }
  }

  return (
    <section id="item-access" className="iam-section item-access" aria-labelledby="item-access-heading">
      <SectionScopeProvider>
        <PageHeader
          id="item-access-heading"
          title="Item access"
          kicker="Access to single items for people without a role, or with more than their role allows. Each grant covers one workload, game server, Docker container or node."
          actions={
            <button className="btn btn-primary btn-sm" type="button" disabled={!access} onClick={openNew}>
              Give access
            </button>
          }
        />
      </SectionScopeProvider>
      {error ? <ErrorNotice error={error} /> : null}
      {!access && !error ? <LoadingState label="Loading item access" /> : null}
      {access ? (
        <>
          {access.grants.length > 0 ? (
            <div className="toolbar">
              <label className="search-field">
                <Icon name="search" size={14} />
                <input
                  className="field-input"
                  type="search"
                  placeholder="Filter by person, group or item"
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  aria-label="Filter item access"
                />
              </label>
            </div>
          ) : null}
          <ResourceTable
            className="item-access-table"
            headers={["Who", "Item", "Access", ""]}
            empty={
              <EmptyState title={access.grants.length ? "No grants match" : "No item access yet"}>
                {access.grants.length
                  ? "Try a different filter."
                  : "Give someone access to one workload, game server, Docker container or node without a role."}
              </EmptyState>
            }
            rows={rows.map((r) => {
              const kind = r.grant.resource_kind as ItemKind;
              return [
                <span key="who">
                  {r.who}
                  {r.grant.subject_kind === "group" ? <span className="tag-inline">Group</span> : null}
                </span>,
                <span key="item">
                  <span className="item-access-kind">{KIND_LABEL[kind] ?? kind}</span>{" "}
                  {r.item ?? (
                    <span className="item-access-missing">
                      <code>{r.grant.resource_id}</code> (missing)
                    </span>
                  )}
                </span>,
                <span key="access" title={r.grant.permissions.map((p) => permissionLabel(kind, p)).join(", ")}>
                  {r.summary}
                </span>,
                <ActionMenu
                  key="act"
                  label={`Actions for ${r.who} on ${r.item ?? r.grant.resource_id}`}
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
      {draft ? (
        <GrantDialog
          draft={draft}
          lists={lists}
          grantable={grantable}
          busy={busy}
          onChange={setDraft}
          onClose={() => setDraft(null)}
          onSave={() => void save()}
        />
      ) : null}
      <ConfirmDialog
        open={Boolean(removing)}
        title="Remove item access"
        danger
        confirmLabel="Remove"
        onClose={() => setRemoving(null)}
        onConfirm={() => void remove()}
      >
        {removing ? (
          <p>
            {subjectName(removing)} loses access to{" "}
            {itemName(removing.resource_kind as ItemKind, removing.resource_id) ?? removing.resource_id}. Access from their role or
            groups is not changed.
          </p>
        ) : null}
      </ConfirmDialog>
    </section>
  );
}

function GrantDialog({
  draft,
  lists,
  grantable,
  busy,
  onChange,
  onClose,
  onSave,
}: {
  draft: Draft;
  lists: Lists;
  grantable: Record<ItemKind, string[]>;
  busy: boolean;
  onChange: (d: Draft) => void;
  onClose: () => void;
  onSave: () => void;
}) {
  const editing = Boolean(draft.id);
  const perms = grantable[draft.kind] ?? [];
  const read = READ_PERMISSION[draft.kind];
  const subjects = draft.subjectKind === "group" ? lists.groups : lists.users;
  const items = lists.items[draft.kind];
  const preset = matchingPreset(draft.kind, draft.perms, perms);
  // Keep an item that no longer exists choosable while editing its grant.
  const itemOptions = draft.itemID && !items.some((o) => o.id === draft.itemID) ? [{ id: draft.itemID, label: `${draft.itemID} (missing)` }, ...items] : items;
  const subjectOptions =
    draft.subjectID && !subjects.some((o) => o.id === draft.subjectID) ? [{ id: draft.subjectID, label: draft.subjectID }, ...subjects] : subjects;

  function toggle(p: string, on: boolean) {
    const next = on ? [...draft.perms, p] : draft.perms.filter((x) => x !== p);
    onChange({ ...draft, perms: perms.filter((x) => next.includes(x) || x === read) });
  }

  return (
    <Dialog
      open
      title={editing ? "Edit item access" : "Give access"}
      onClose={onClose}
      footer={
        <div className="btn-row">
          <button
            className="btn btn-primary"
            type="submit"
            form="item-access-form"
            disabled={busy || !draft.subjectID || !draft.itemID}
          >
            {editing ? "Save" : "Give access"}
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
        <div className="field">
          <span className="field-label">Who</span>
          <Segmented<SubjectKind>
            ariaLabel="Give access to"
            value={draft.subjectKind}
            options={[
              { id: "user", label: "User", disabled: editing },
              { id: "group", label: "Group", disabled: editing },
            ]}
            onChange={(v) => onChange({ ...draft, subjectKind: v, subjectID: "" })}
          />
        </div>
        <SelectField
          id="item-access-subject"
          label={draft.subjectKind === "group" ? "Group" : "User"}
          value={draft.subjectID}
          disabled={editing}
          required
          onChange={(e) => onChange({ ...draft, subjectID: e.target.value })}
        >
          <option value="">{draft.subjectKind === "group" ? "Choose a group" : "Choose a user"}</option>
          {subjectOptions.map((o) => (
            <option key={o.id} value={o.id}>
              {o.label}
            </option>
          ))}
        </SelectField>
        <div className="field">
          <span className="field-label">Kind of item</span>
          <Segmented<ItemKind>
            ariaLabel="Kind of item"
            value={draft.kind}
            options={KIND_ORDER.map((k) => ({ id: k, label: KIND_LABEL[k], disabled: editing }))}
            onChange={(k) => onChange({ ...draft, kind: k, itemID: "", perms: presetPermissions(k, "view", grantable[k] ?? []) })}
          />
        </div>
        <SelectField
          id="item-access-item"
          label={KIND_LABEL[draft.kind]}
          value={draft.itemID}
          disabled={editing}
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
        {draft.kind === "node" ? (
          <p className="banner banner-warn" role="note">
            Terminal and file access on a node are root access to that machine: everything on it, including other workloads and
            their data.
          </p>
        ) : null}
        <div className="field">
          <span className="field-label">Access</span>
          <div className="btn-row is-flush" role="group" aria-label="Presets">
            {(["view", "operate", "full"] as PresetId[]).map((id) => (
              <button
                key={id}
                type="button"
                className={"btn btn-sm " + (preset === id ? "btn-primary" : "btn-ghost")}
                aria-pressed={preset === id}
                onClick={() => onChange({ ...draft, perms: presetPermissions(draft.kind, id, perms) })}
              >
                {PRESET_LABEL[id]}
              </button>
            ))}
          </div>
          {GROUP_ORDER.map((group) => {
            const inGroup = perms.filter((p) => permissionGroup(p) === group);
            if (inGroup.length === 0) {
              return null;
            }
            return (
              <fieldset key={group} className="item-access-perms">
                <legend>{group}</legend>
                {inGroup.map((p) => (
                  <label key={p} className="check">
                    <input
                      type="checkbox"
                      checked={p === read || draft.perms.includes(p)}
                      disabled={p === read}
                      onChange={(e) => toggle(p, e.target.checked)}
                    />
                    <span>
                      {permissionLabel(draft.kind, p)}
                      {p === read ? <span className="field-hint"> (always included)</span> : null}
                    </span>
                  </label>
                ))}
              </fieldset>
            );
          })}
        </div>
      </form>
    </Dialog>
  );
}
