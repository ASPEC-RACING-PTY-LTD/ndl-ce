import { Link } from "../components/Link";

const DOCS = [
  {
    id: "ce",
    title: "CE 1.0",
    summary: "What CE 1.0 includes. No Cloud or EE key required. No EE blobs. Hardware gates are not proven on this host.",
  },
  { id: "install", title: "Install", summary: "One-line bootstrap, manual repo, and installer ISO." },
  { id: "uninstall", title: "Uninstall", summary: "apt remove does not delete workload data." },
  { id: "recovery", title: "Recovery", summary: "Stopping ndl-control or ndl-agent does not stop guests." },
  { id: "backup", title: "Backup", summary: "Snapshots are not backups. Restore as new versus replace." },
  { id: "cluster", title: "Cluster", summary: "Join tokens, placement, migrate, single-writer HA." },
  { id: "store", title: "Store", summary: "Declarative manifests. No helper scripts." },
  { id: "ai", title: "AI", summary: "Ask, Plan, Operate, Automate. Not a shell." },
  {
    id: "checklists",
    title: "Checklists",
    summary: "Virt and physical CE 1.0 checklists are documents, not proof this host ran them.",
  },
];

export function DocsPage() {
  return (
    <section className="page">
      <header className="page-header">
        <h1>Docs</h1>
        <p className="lede">
          Operator runbooks shipped in the tree under docs/. License activation is not required. CE 1.0 hardware gates
          are not proven on this host. This tree does not ship EE blobs. Ubuntu is not claimed as Tier 1. Signed
          production packages use the HTTPS apt repo documented in install.md.
        </p>
      </header>
      <article className="panel">
        <ul className="plain-list">
          {DOCS.map((doc) => (
            <li key={doc.id}>
              <h2>{doc.title}</h2>
              <p>{doc.summary}</p>
            </li>
          ))}
        </ul>
        <p>
          License surface: <Link href="/settings/license">License</Link>.
        </p>
      </article>
    </section>
  );
}
