import { Fragment } from "react";
import { formatRam } from "./caps";
import { VERIFICATION_INFO, requirementStageLabel } from "./catalogue";
import { GameArt } from "./CatalogueBrowser";
import type { CatalogueItem, GamePort, GameTemplate } from "./types";

export function portLabel(port: GamePort): string {
  const base = `${port.host_port || port.container_port}/${port.protocol || "tcp"}`;
  return port.name ? `${port.name} ${base}` : base;
}

function safeHttps(url?: string): string | undefined {
  return url && /^https:\/\//i.test(url) ? url : undefined;
}

/** What the picked template will do, shown next to the Options form. */
export function TemplateSummary({ template, art }: { template: GameTemplate; art?: CatalogueItem | null }) {
  const fixed = (template.variables ?? []).filter((v) => v.editable === false && v.viewable !== false && v.default);
  const verify = template.verification ? VERIFICATION_INFO[template.verification] : undefined;
  const ports = template.default_ports ?? [];
  const requirements = template.requirements ?? [];
  const dependencies = template.dependencies ?? [];
  const notes = (template.notes ?? []).filter(Boolean);
  const docs = safeHttps(template.docs_url);
  const resources = [
    template.default_cpus ? `${template.default_cpus} CPU` : "",
    template.default_memory_mb ? `${formatRam(template.default_memory_mb * 1024 * 1024)} RAM` : "",
    template.default_disk_mb ? `${formatRam(template.default_disk_mb * 1024 * 1024)} disk` : "",
  ].filter(Boolean);
  const title = template.game_title && template.game_title !== template.name ? template.game_title : "";

  return (
    <aside className="gs-summary" aria-label="Template summary">
      {art ? (
        <GameArt
          url={art.game_logo_url || art.logo_url}
          kind={art.game_logo_url ? art.game_logo_kind : art.logo_kind}
          title={template.game_title || template.name}
          size="hero"
        />
      ) : null}
      <p className="gs-summary-title">
        <strong>{template.name}</strong>
        {title ? <span className="gs-meta"> {title}</span> : null}
      </p>
      {template.summary ? <p className="gs-meta">{template.summary}</p> : null}
      <dl className="gs-summary-list">
        {fixed.map((v) => (
          <Fragment key={v.env}>
            <dt>{v.name}</dt>
            <dd>{v.default}</dd>
          </Fragment>
        ))}
        {template.install_method ? (
          <>
            <dt>Install method</dt>
            <dd>{template.install_method}</dd>
          </>
        ) : null}
        {template.update_procedure ? (
          <>
            <dt>Updates</dt>
            <dd>{template.update_procedure}</dd>
          </>
        ) : null}
        {ports.length > 0 ? (
          <>
            <dt>Ports</dt>
            <dd>
              {ports.map((p) => (
                <span key={`${p.container_port}/${p.protocol ?? "tcp"}/${p.name ?? ""}`} className="gs-badge">
                  {portLabel(p)}
                  {p.fixed ? " (fixed)" : ""}
                </span>
              ))}
            </dd>
          </>
        ) : null}
        {resources.length > 0 ? (
          <>
            <dt>Recommended</dt>
            <dd>
              {resources.join(", ")}
              {template.min_memory_mb ? `. Minimum ${formatRam(template.min_memory_mb * 1024 * 1024)} RAM` : ""}
            </dd>
          </>
        ) : null}
        {template.architectures?.length ? (
          <>
            <dt>Platforms</dt>
            <dd>{template.architectures.join(", ")}</dd>
          </>
        ) : null}
        {template.engine ? (
          <>
            <dt>Engine</dt>
            <dd>{template.engine}</dd>
          </>
        ) : null}
        {verify ? (
          <>
            <dt>Verification</dt>
            <dd>
              <span className={"gs-badge is-verify is-" + template.verification} title={verify.title}>
                {verify.label}
              </span>{" "}
              <span className="gs-meta">{verify.title}</span>
              {template.source_ref ? <span className="gs-meta"> Reference: {template.source_ref}</span> : null}
            </dd>
          </>
        ) : null}
      </dl>
      {requirements.length > 0 ? (
        <div>
          <p className="gs-summary-head">Requirements</p>
          <ul className="gs-summary-reqs">
            {requirements.map((r) => {
              const href = safeHttps(r.url);
              return (
                <li key={`${r.kind}:${r.stage}:${r.env ?? ""}`}>
                  {href ? (
                    <a href={href} target="_blank" rel="noreferrer noopener">
                      {r.label}
                    </a>
                  ) : (
                    r.label
                  )}{" "}
                  <span className="gs-meta">({requirementStageLabel(r.stage)})</span>
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}
      {dependencies.length > 0 ? (
        <div>
          <p className="gs-summary-head">Prerequisites installed with the server</p>
          <p className="gs-meta">{dependencies.join(", ")}</p>
        </div>
      ) : null}
      {notes.length > 0 ? (
        <div>
          <p className="gs-summary-head">Notes</p>
          <ul className="gs-summary-reqs">
            {notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </div>
      ) : null}
      {docs ? (
        <p>
          <a href={docs} target="_blank" rel="noreferrer noopener">
            Server documentation
          </a>
        </p>
      ) : null}
    </aside>
  );
}
