import { useEffect } from "react";
import { Shell } from "./components/Shell";
import { DashboardPage } from "./pages/DashboardPage";
import { EventsPage } from "./pages/EventsPage";
import { LoginPage } from "./pages/LoginPage";
import { MePage } from "./pages/MePage";
import { NodePage } from "./pages/NodePage";
import { SetupPage } from "./pages/SetupPage";
import { NetworkPage } from "./pages/NetworkPage";
import { StoragePage } from "./pages/StoragePage";
import { TasksPage } from "./pages/TasksPage";
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

  let page = <DashboardPage />;
  if (path === "/me") {
    page = <MePage />;
  } else if (path === "/tasks") {
    page = <TasksPage />;
  } else if (path === "/events" || path === "/node/events") {
    page = <EventsPage />;
  } else if (path === "/storage") {
    page = <StoragePage />;
  } else if (path === "/network") {
    page = <NetworkPage />;
  } else if (path === "/node" || path.startsWith("/node/")) {
    page = <NodePage />;
  }

  return <Shell>{page}</Shell>;
}

export function App() {
  return (
    <SessionProvider>
      <AppRoutes />
    </SessionProvider>
  );
}
