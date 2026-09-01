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
import { FilesPage } from "./pages/FilesPage";
import { TerminalPage } from "./pages/TerminalPage";
import { WorkloadCreatePage } from "./pages/WorkloadCreatePage";
import { WorkloadDetailPage } from "./pages/WorkloadDetailPage";
import { WorkloadsPage } from "./pages/WorkloadsPage";
import { VmCreatePage } from "./pages/VmCreatePage";
import { ConsolePage } from "./pages/ConsolePage";
import { CertificatePage } from "./pages/CertificatePage";
import { SnapshotsPage } from "./pages/SnapshotsPage";
import { BackupsPage } from "./pages/BackupsPage";
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
  } else if (path === "/workloads/new/system-container") {
    page = <WorkloadCreatePage />;
  } else if (path === "/workloads/new/vm") {
    page = <VmCreatePage />;
  } else if (/^\/workloads\/[^/]+\/console$/.test(path)) {
    page = <ConsolePage />;
  } else if (/^\/workloads\/[^/]+\/terminal$/.test(path)) {
    page = <TerminalPage />;
  } else if (/^\/workloads\/[^/]+\/files$/.test(path)) {
    page = <FilesPage />;
  } else if (/^\/workloads\/[^/]+\/snapshots$/.test(path)) {
    page = <SnapshotsPage />;
  } else if (/^\/nodes\/[^/]+\/terminal$/.test(path)) {
    page = <TerminalPage />;
  } else if (/^\/nodes\/[^/]+\/files$/.test(path)) {
    page = <FilesPage />;
  } else if (path.startsWith("/workloads/") && path !== "/workloads") {
    page = <WorkloadDetailPage />;
  } else if (path === "/workloads") {
    page = <WorkloadsPage />;
  } else if (path === "/node" || path.startsWith("/node/")) {
    page = <NodePage />;
  } else if (path === "/settings/certificates") {
    page = <CertificatePage />;
  } else if (path === "/backups") {
    page = <BackupsPage />;
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
