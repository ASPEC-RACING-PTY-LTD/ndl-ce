import { PageHeader } from "../components/PageHeader";
import { RegistriesPanel } from "../components/ConfigPanels";

export function RegistriesPage() {
  return (
    <section className="page" aria-labelledby="registries-page-heading">
      <PageHeader id="registries-page-heading" title="Registries" kicker="Container registries OCI workloads pull from. Credentials are stored on the appliance and never shown again." />
      <RegistriesPanel />
    </section>
  );
}
