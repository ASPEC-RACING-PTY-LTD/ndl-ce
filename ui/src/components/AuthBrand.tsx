import { BrandMark } from "./BrandMark";

export function AuthBrand() {
  return (
    <header className="auth-brand">
      <BrandMark size="auth" />
      <p className="wordmark">No-dal</p>
      <p className="auth-edition">Community Edition</p>
    </header>
  );
}
