import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";

export function NotFoundPage() {
  return (
    <section className="page" aria-labelledby="not-found-heading">
      <PageHeader id="not-found-heading" title="Not found" kicker="That page is not part of this appliance." />
      <p>
        <Link href="/">Back to Dashboard</Link>
      </p>
    </section>
  );
}
