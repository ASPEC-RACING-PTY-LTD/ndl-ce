import { useEffect } from "react";
import { Shell } from "./components/Shell";
import { DashboardPage } from "./pages/DashboardPage";
import { EventsPage } from "./pages/EventsPage";
import { LoginPage } from "./pages/LoginPage";
import { MePage } from "./pages/MePage";
import { NodePage } from "./pages/NodePage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { SetupPage } from "./pages/SetupPage";
import { NetworkPage } from "./pages/NetworkPage";
import { StoragePage } from "./pages/StoragePage";
import { FilesPage } from "./pages/FilesPage";
import { TerminalPage } from "./pages/TerminalPage";
import { WorkloadCreatePage } from "./pages/WorkloadCreatePage";
import { WorkloadDetailPage } from "./pages/WorkloadDetailPage";
import { WorkloadsPage } from "./pages/WorkloadsPage";
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

function matchPage(path: string) {
  if (path === "/me") {
    return <MePage />;
  }
  if (path === "/tasks") {
    return <TasksPage />;
  }
  if (path === "/events" || path === "/node/events") {
    return <EventsPage />;
  }
  if (path === "/storage") {
    return <StoragePage />;
  }
  if (path === "/network") {
    return <NetworkPage />;
  }
  if (path === "/workloads/new/system-container") {
    return <WorkloadCreatePage />;
  }
  if (/^\/workloads\/[^/]+\/(terminal|files|settings|snapshots)$/.test(path)) {
    if (path.endsWith("/terminal")) {
      return <TerminalPage />;
    }
    if (path.endsWith("/files")) {
      return <FilesPage />;
    }
    return <WorkloadDetailPage />;
  }
  if (/^\/nodes\/[^/]+\/(terminal|files|hardware|metrics)$/.test(path) || /^\/nodes\/[^/]+$/.test(path)) {
    if (path.endsWith("/terminal")) {
      return <TerminalPage />;
    }
    if (path.endsWith("/files")) {
      return <FilesPage />;
    }
    return <NodePage />;
  }
  if (path.startsWith("/workloads/") && path !== "/workloads") {
    return <WorkloadDetailPage />;
  }
  if (path === "/workloads") {
    return <WorkloadsPage />;
  }
  if (path === "/node" || path.startsWith("/node/")) {
    return <NodePage />;
  }
  if (path === "/") {
    return <DashboardPage />;
  }
  return <NotFoundPage />;
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
        <main className="panel auth-panel" aria-labelledby="session-error-heading">
          <h1 id="session-error-heading">Cannot reach the appliance</h1>
          <p className="banner banner-error" role="alert">
            {session.message}
          </p>
          <button className="btn btn-primary" type="button" onClick={() => void session.refresh()}>
            Try again
          </button>
        </main>
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

  return <Shell>{matchPage(path)}</Shell>;
}

export function App() {
  return (
    <SessionProvider>
      <AppRoutes />
    </SessionProvider>
  );
}
