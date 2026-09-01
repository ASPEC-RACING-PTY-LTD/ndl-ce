import { useEffect } from "react";
import { Shell } from "./components/Shell";
import { DashboardPage } from "./pages/DashboardPage";
import { LoginPage } from "./pages/LoginPage";
import { MePage } from "./pages/MePage";
import { SetupPage } from "./pages/SetupPage";
import { navigate, usePath } from "./router";
import { SessionProvider, useSession } from "./session";

function GateNotice({ children }: { children: string }) {
  return (
    <div className="auth-screen">
      <p className="gate-status" role="status">
        {children}
      </p>
    </div>
  );
}

function Redirect({ to }: { to: string }) {
  useEffect(() => {
    navigate(to, { replace: true });
  }, [to]);

  return <GateNotice>Redirecting</GateNotice>;
}

function AppRoutes() {
  const path = usePath();
  const session = useSession();

  if (session.status === "loading") {
    return <GateNotice>Loading</GateNotice>;
  }

  if (session.status === "error") {
    return (
      <div className="auth-screen">
        <section className="panel auth-panel" aria-labelledby="session-error-heading">
          <h1 id="session-error-heading">Cannot reach the appliance</h1>
          <p className="banner banner-error" role="alert">
            {session.message}
          </p>
          <button className="btn btn-primary" type="button" onClick={() => void session.refresh()}>
            Try again
          </button>
        </section>
      </div>
    );
  }

  if (path === "/setup") {
    if (!session.setupOpen) {
      return <Redirect to="/login" />;
    }
    return <SetupPage />;
  }

  if (path === "/login") {
    if (session.user) {
      return <Redirect to="/" />;
    }
    return <LoginPage />;
  }

  if (!session.user) {
    return <Redirect to={session.setupOpen ? "/setup" : "/login"} />;
  }

  return (
    <Shell>{path === "/me" ? <MePage /> : <DashboardPage />}</Shell>
  );
}

export function App() {
  return (
    <SessionProvider>
      <AppRoutes />
    </SessionProvider>
  );
}
