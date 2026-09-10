import { Link } from "../components/Link";
import { PageHeader } from "../components/PageHeader";

export function ForbiddenPage() {
  return (
    <section className="page" aria-labelledby="forbidden-heading">
      <PageHeader
        id="forbidden-heading"
        title="Forbidden"
        kicker="This page is limited to accounts with Management permission."
      />
      <p>Your role cannot open this page. The API also refuses the same request.</p>
      <p>
        <Link href="/">Back to Dashboard</Link>
      </p>
    </section>
  );
}
