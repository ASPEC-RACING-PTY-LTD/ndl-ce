import { useEffect } from "react";
import { AuthBrand } from "./components/AuthBrand";
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
import { HostOperatePage } from "./pages/HostOperatePage";
import { TerminalPage } from "./pages/TerminalPage";
import { TerminalWorkspaceProvider } from "./terminal/workspace";
import { TerminalWorkspacePage } from "./pages/TerminalWorkspacePage";
import { WorkloadCreatePage } from "./pages/WorkloadCreatePage";
import { OciCreatePage } from "./pages/OciCreatePage";
import { WorkloadDetailPage } from "./pages/WorkloadDetailPage";
import { WorkloadsPage } from "./pages/WorkloadsPage";
import { VmCreatePage } from "./pages/VmCreatePage";
import { TemplatesPage } from "./pages/TemplatesPage";
import { ImportVMPage } from "./pages/ImportVMPage";
import { ConsolePage } from "./pages/ConsolePage";
import { CertificatePage } from "./pages/CertificatePage";
import { ClusterPage } from "./pages/ClusterPage";
import { FeaturesPage } from "./pages/FeaturesPage";
import { KubernetesPage } from "./pages/KubernetesPage";
import { StorePage } from "./pages/StorePage";
import { AutomationPage } from "./pages/AutomationPage";
import { AskPage } from "./pages/AskPage";
import { PlansPage } from "./pages/PlansPage";
import { LicensePage } from "./pages/LicensePage";
import { DocsPage } from "./pages/DocsPage";
import { SnapshotsPage } from "./pages/SnapshotsPage";
import { BackupsPage } from "./pages/BackupsPage";
import { UpdatesPage } from "./pages/UpdatesPage";
import { MFAPage } from "./pages/MFAPage";
import { GroupsPage } from "./pages/GroupsPage";
import { AuditPage } from "./pages/AuditPage";
import { GpuPage } from "./pages/GpuPage";
import { StacksPage, StackDetailPage } from "./pages/StacksPage";
import { TasksPage } from "./pages/TasksPage";
import { AlertsPage } from "./pages/AlertsPage";
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
  if (path === "/alerts") {
    return <AlertsPage />;
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
  if (path === "/workloads/new/oci") {
    return <OciCreatePage />;
  }
  if (path === "/workloads/new/vm") {
    return <VmCreatePage />;
  }
  if (path === "/workloads/import") {
    return <ImportVMPage />;
  }
  if (path === "/templates") {
    return <TemplatesPage />;
  }
  if (path === "/terminal") {
    return <TerminalWorkspacePage />;
  }
  if (/^\/workloads\/[^/]+\/console$/.test(path)) {
    return <ConsolePage />;
  }
  if (/^\/workloads\/[^/]+\/terminal$/.test(path)) {
    return <TerminalPage />;
  }
  if (/^\/workloads\/[^/]+\/files$/.test(path)) {
    return <FilesPage />;
  }
  if (/^\/workloads\/[^/]+\/snapshots$/.test(path)) {
    return <SnapshotsPage />;
  }
  if (/^\/nodes\/[^/]+\/terminal$/.test(path)) {
    return <TerminalPage />;
  }
  if (/^\/nodes\/[^/]+\/files$/.test(path)) {
    return <FilesPage />;
  }
  if (/^\/nodes\/[^/]+$/.test(path)) {
    return <HostOperatePage />;
  }
  if (/^\/workloads\/[^/]+\/gpus$/.test(path)) {
    return <GpuPage />;
  }
  if (path.startsWith("/workloads/") && path !== "/workloads") {
    return <WorkloadDetailPage />;
  }
  if (path === "/workloads") {
    return <WorkloadsPage />;
  }
  if (path === "/stacks") {
    return <StacksPage />;
  }
  if (path.startsWith("/stacks/")) {
    return <StackDetailPage />;
  }
  if (path === "/node" || path.startsWith("/node/")) {
    return <NodePage />;
  }
  if (path === "/settings/cluster") {
    return <ClusterPage />;
  }
  if (path === "/settings/features") {
    return <FeaturesPage />;
  }
  if (path === "/settings/kubernetes") {
    return <KubernetesPage />;
  }
  if (path === "/store") {
    return <StorePage />;
  }
  if (path === "/automation") {
    return <AutomationPage />;
  }
  if (path === "/ask") {
    return <AskPage />;
  }
  if (path === "/plans") {
    return <PlansPage />;
  }
  if (path === "/settings/license") {
    return <LicensePage />;
  }
  if (path === "/docs") {
    return <DocsPage />;
  }
  if (path === "/settings/certificates") {
    return <CertificatePage />;
  }
  if (path === "/settings/updates") {
    return <UpdatesPage />;
  }
  if (path === "/settings/mfa") {
    return <MFAPage />;
  }
  if (path === "/groups") {
    return <GroupsPage />;
  }
  if (path === "/audit") {
    return <AuditPage />;
  }
  if (path === "/backups") {
    return <BackupsPage />;
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
        <AuthBrand />
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

  return (
    <Shell>
      <TerminalWorkspaceProvider>{matchPage(path)}</TerminalWorkspaceProvider>
    </Shell>
  );
}

export function App() {
  return (
    <SessionProvider>
      <AppRoutes />
    </SessionProvider>
  );
}
