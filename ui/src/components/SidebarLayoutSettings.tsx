import { allowedByRbac } from "../nav/disclosure";
import { useNavDisclosure } from "../nav/NavDisclosure";
import { NAV_MODULES, TEMPLATES } from "../nav/modules";
import { useSession } from "../session";
import { Checkbox } from "../ui/Checkbox";
import { SelectionCard } from "../ui/SelectionCard";

/**
 * SidebarLayoutSettings picks which entries the sidebar shows (Simple,
 * Advanced or Custom) and, for Custom, which modules and in what order. It
 * never turns a feature on or off.
 */
export function SidebarLayoutSettings() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const { prefs, capabilityOn, setTemplate, setCustomModule, reorderCustom } = useNavDisclosure();
  const customIds = prefs.customOrder.length > 0 ? prefs.customOrder : prefs.customVisible;
  const customModules = NAV_MODULES.filter(
    (mod) => allowedByRbac(mod, user?.roles, user?.grants) && (!mod.capability || capabilityOn(mod.capability)),
  );

  return (
    <>
      <article className="panel stack">
        <h2>Sidebar layout</h2>
        <p className="field-hint">
          Which entries the sidebar shows. This does not turn features on or off; a disabled feature never shows.
        </p>
        <div className="feature-templates" role="radiogroup" aria-label="Navigation template">
          {TEMPLATES.map((item) => (
            <SelectionCard
              key={item.id}
              role="radio"
              title={item.label}
              description={item.summary}
              selected={prefs.template === item.id}
              onSelect={() => setTemplate(item.id)}
            />
          ))}
        </div>
      </article>

      {prefs.template === "custom" ? (
        <article className="panel stack">
          <h2>Custom modules</h2>
          <p className="lede">Hide or show sidebar entries and set their order.</p>
          <ul className="plain-list">
            {customModules.map((mod) => {
              const checked = prefs.customVisible.includes(mod.id);
              const idx = customIds.indexOf(mod.id);
              return (
                <li key={mod.id} className="inline-actions">
                  <Checkbox
                    id={`mod-${mod.id}`}
                    label={mod.label}
                    checked={checked}
                    onChange={(e) => setCustomModule(mod.id, e.target.checked)}
                  />
                  {checked && idx >= 0 ? (
                    <>
                      <button type="button" className="btn btn-secondary" onClick={() => reorderCustom(mod.id, -1)}>
                        Up
                      </button>
                      <button type="button" className="btn btn-secondary" onClick={() => reorderCustom(mod.id, 1)}>
                        Down
                      </button>
                    </>
                  ) : null}
                </li>
              );
            })}
          </ul>
        </article>
      ) : null}
    </>
  );
}
