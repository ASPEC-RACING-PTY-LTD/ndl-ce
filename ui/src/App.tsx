import { useEffect, type ReactNode } from "react";
import { AuthBrand } from "./components/AuthBrand";
import { Shell } from "./components/Shell";
import { RequireGrant } from "./components/RequireGrant";
import { FeatureGate } from "./components/FeatureGate";
import { AccessControlArea } from "./iam/AccessControlArea";
import { UsersPage } from "./pages/UsersPage";
import { IAMLayout, iamSections } from "./iam/IAMLayout";
import { ForbiddenPage } from "./pages/ForbiddenPage";
import { DashboardPage } from "./pages/DashboardPage";
import { EventsPage } from "./pages/EventsPage";
import { LoginPage } from "./pages/LoginPage";
import { MePage } from "./pages/MePage";
import { NodePage } from "./pages/NodePage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { SetupPage } from "./pages/SetupPage";
import { RecoverPage } from "./pages/RecoverPage";
import { NetworkHubPage, type NetworkTab } from "./pages/NetworkHubPage";
import { FilesPage } from "./pages/FilesPage";
import { HostOperatePage } from "./pages/HostOperatePage";
import { TerminalPage } from "./pages/TerminalPage";
import { TerminalWorkspaceProvider } from "./terminal/workspace";
import { TerminalWorkspacePage } from "./pages/TerminalWorkspacePage";
import { FilesWorkspacePage } from "./pages/FilesWorkspacePage";
import { WorkloadCreatePage } from "./pages/WorkloadCreatePage";
import { OciCreatePage } from "./pages/OciCreatePage";
import { WorkloadDetailPage } from "./pages/WorkloadDetailPage";
import { WorkloadDiagnosticsPage } from "./pages/WorkloadDiagnosticsPage";
import { WorkloadStoragePage } from "./pages/WorkloadStoragePage";
import { WorkloadsPage } from "./pages/WorkloadsPage";
import { VmCreatePage } from "./pages/VmCreatePage";
import { TemplatesPage } from "./pages/TemplatesPage";
import { ImportVMPage } from "./pages/ImportVMPage";
import { ImportExportPage } from "./pages/ImportExportPage";
import { ConsolePage } from "./pages/ConsolePage";
import { CertificatePage } from "./pages/CertificatePage";
import { ClusterPage } from "./pages/ClusterPage";
import { FeaturesPage } from "./pages/FeaturesPage";
import { KubernetesPage } from "./pages/KubernetesPage";
import { StorePage } from "./pages/StorePage";
import { AutomationPage } from "./pages/AutomationPage";
import { ApprovalsPage } from "./iam/ApprovalsPage";
import { AutomationHome } from "./pages/automation/AutomationHome";
import { NotificationsPage } from "./pages/NotificationsPage";
import { FlowEditorPage } from "./pages/automation/FlowEditorPage";
import { RunPage } from "./pages/automation/RunPage";
import { AskPage } from "./pages/AskPage";
import { PlansPage } from "./pages/PlansPage";
import { LicensePage } from "./pages/LicensePage";
import { DocsPage } from "./pages/DocsPage";
import { SnapshotsPage } from "./pages/SnapshotsPage";
import { BackupsPage } from "./pages/BackupsPage";
import { UpdatesPage } from "./pages/UpdatesPage";
import { RegistriesPage } from "./pages/RegistriesPage";
import { AuditPage } from "./pages/AuditPage";
import { GpuPage } from "./pages/GpuPage";
import { OciPage } from "./pages/OciPage";
import { CodeholdPage } from "./pages/CodeholdPage";
import { DockerHubPage } from "./pages/DockerHubPage";
import { WorkloadExtensionsPage } from "./pages/WorkloadExtensionsPage";
import { StorageHubPage } from "./pages/StorageHubPage";
import { GameServersHomePage } from "./pages/GameServersHomePage";
import { GameServerCreatePage } from "./pages/GameServerCreatePage";
import { GameServerDetailPage } from "./pages/GameServerDetailPage";
import { AlertsPage } from "./pages/AlertsPage";
import { navigate, usePath } from "./router";
import { isGameServersCreatePath, isGameServersHomePath, legacyGameServersRedirect } from "./nav/gameServers";
import { GameServerFrame } from "./nav/GameServerFrame";
import { NavDisclosureProvider } from "./nav/NavDisclosure";
import { WorkloadFrame } from "./nav/WorkloadFrame";
import { selectedTargetFromPath } from "./nav/match";
import { isGameServersContext } from "./nav/gameServers";
import { SessionProvider, useSession } from "./session";
import { ErrorNotice } from "./components/ErrorNotice";
import { GlobalContextMenu } from "./components/ContextMenu";
import { bindPrefsUser, unbindPrefs } from "./prefs";
import { loadAppearanceDefaults, resetAppearanceDefaults, useInstanceLayout } from "./appearance/state";
import { AdminHome } from "./nav/AdminDashboard";
import { AreaFrame } from "./nav/AreaFrames";
import { ThemePage } from "./pages/ThemePage";
import { ThemeManagementPage } from "./pages/admin/ThemeManagementPage";
import { SearchManagementPage } from "./pages/admin/SearchManagementPage";
import { InstanceHomePage } from "./pages/InstanceHomePage";
import { ComponentsPage } from "./pages/ComponentsPage";
import { CodeholdCLIDialog } from "./components/CodeholdCLIDialog";
import { ProfilePage, PublicProfilePage } from "./pages/ProfilePage";
import { canMutate } from "./rbac";

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

/** /iam opens the first IAM section the user may see. */
function IAMHome() {
  const session = useSession();
  const first = iamSections(session.status === "ready" ? session.user : null)[0];
  if (!first) {
    return <ForbiddenPage />;
  }
  return <Redirect to={first.href} />;
}

/** WorkloadsRoute is /workloads: system containers only in the Instance layout, everything in the classic one. */
function WorkloadsRoute() {
  const [layout] = useInstanceLayout();
  return <WorkloadsPage fixedKind={layout === "instance" ? "system-container" : undefined} />;
}

function CodeholdCLIRoute() {
  const session = useSession();
  return <CodeholdCLIDialog open asPage canChange={canMutate(session.status === "ready" ? session.user?.roles : undefined)} onClose={() => undefined} />;
}

function matchPage(path: string) {
  if (path === "/me") {
    return <MePage />;
  }
  if (path === "/profile") {
    return <ProfilePage />;
  }
  if (/^\/people\/[^/]+$/.test(path)) {
    return <PublicProfilePage key={path} id={decodeURIComponent(path.split("/")[2])} />;
  }
  if (path === "/theme" || path.startsWith("/theme/")) {
    return <ThemePage />;
  }
  if (path === "/admin") {
    return <AdminHome />;
  }
  if (path === "/admin/theme") {
    return (
      <RequireGrant permission="settings.appearance.manage">
        <ThemeManagementPage />
      </RequireGrant>
    );
  }
  if (path === "/admin/search") {
    return (
      <RequireGrant permission="search.manage">
        <SearchManagementPage />
      </RequireGrant>
    );
  }
  if (path === "/instance") {
    return <InstanceHomePage />;
  }
  if (path === "/instance/vms") {
    return <WorkloadsPage fixedKind="vm" />;
  }
  if (path === "/instance/components") {
    return <ComponentsPage />;
  }
  if (path === "/codehold/cli") {
    return <CodeholdCLIRoute />;
  }
  if (path === "/tasks") {
    // Tasks live in the Audit Log; this keeps old links working.
    return <AuditPage initialSource="task" />;
  }
  if (path === "/events") {
    return <EventsPage />;
  }
  if (path === "/alerts") {
    return <AlertsPage />;
  }
  if (path === "/storage" || path === "/storage/manage") {
    return <StorageHubPage tab={path === "/storage/manage" ? "manage" : "storage"} />;
  }
  if (path === "/network" || path === "/network/edge" || path === "/network/ddos" || path === "/network/cloudflare" || path === "/network/caddy" || path === "/network/nginx") {
    return <NetworkHubPage tab={(path.split("/")[2] as NetworkTab | undefined) ?? "network"} />;
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
  const legacyGame = legacyGameServersRedirect(path, window.location.search);
  if (legacyGame) {
    return <Redirect to={legacyGame} />;
  }
  if (path === "/game-servers/fleet") {
    // Fleet was a copy of the server list; bulk actions live there now.
    return <Redirect to="/game-servers" />;
  }
  if (path === "/game-servers/catalogue") {
    return <Redirect to="/game-servers/create" />;
  }
  if (isGameServersHomePath(path)) {
    return <GameServersHomePage />;
  }
  if (isGameServersCreatePath(path)) {
    return <GameServerCreatePage />;
  }
  if (path.startsWith("/game-servers/")) {
    return (
      <GameServerFrame>
        <GameServerDetailPage />
      </GameServerFrame>
    );
  }
  if (path === "/import-export") {
    return <ImportExportPage />;
  }
  if (path === "/templates") {
    return <TemplatesPage />;
  }
  if (path === "/registries") {
    return <RegistriesPage />;
  }
  if (path === "/files") {
    return <FilesWorkspacePage />;
  }
  if (path === "/terminal") {
    return <TerminalWorkspacePage />;
  }
  if (/^\/workloads\/[^/]+\/console$/.test(path)) {
    return <ConsolePage />;
  }
  if (/^\/workloads\/[^/]+\/terminal$/.test(path)) {
    // Keyed by path so switching workloads starts from a clean state.
    return <TerminalPage key={path} />;
  }
  if (/^\/workloads\/[^/]+\/files$/.test(path)) {
    return <FilesPage />;
  }
  if (/^\/workloads\/[^/]+\/snapshots$/.test(path)) {
    return <SnapshotsPage />;
  }
  if (/^\/nodes\/[^/]+\/terminal$/.test(path)) {
    return <TerminalPage key={path} />;
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
  if (/^\/workloads\/[^/]+\/diagnostics$/.test(path)) {
    return <WorkloadDiagnosticsPage />;
  }
  if (/^\/workloads\/[^/]+\/extensions$/.test(path)) {
    return <WorkloadExtensionsPage />;
  }
  if (/^\/workloads\/[^/]+\/storage$/.test(path)) {
    return <WorkloadStoragePage />;
  }
  if (path.startsWith("/workloads/") && path !== "/workloads") {
    return <WorkloadDetailPage />;
  }
  if (path === "/workloads") {
    return <WorkloadsRoute />;
  }
  if (path === "/uptime" || path === "/docker/uptime") {
    // Uptime lives on the Docker page; the old address still lands there.
    if (path === "/uptime") {
      window.history.replaceState({}, "", "/docker/uptime");
    }
    return <DockerHubPage tab="uptime" />;
  }
  if (path === "/cloudflare") {
    // Cloudflare moved under Network; the old address still lands there.
    window.history.replaceState({}, "", "/network/cloudflare");
    return <NetworkHubPage tab="cloudflare" />;
  }
  if (path === "/codehold") {
    return <CodeholdPage />;
  }
  if (path === "/oci") {
    return <OciPage />;
  }
  if (path === "/stacks" || path.startsWith("/stacks/")) {
    // Stacks became OCI container groups; old links land on the OCI page.
    window.history.replaceState({}, "", "/oci");
    return <OciPage />;
  }
  if (path === "/docker/deploy") {
    return <DockerHubPage tab="deploy" />;
  }
  if (path === "/docker" || path.startsWith("/docker/")) {
    return <DockerHubPage tab="docker" />;
  }
  if (path === "/node" || path.startsWith("/node/")) {
    return <NodePage />;
  }
  if (path === "/settings/cluster") {
    return <ClusterPage />;
  }
  if (path === "/settings/features") {
    return (
      <RequireGrant permission="feature.manage">
        <FeaturesPage />
      </RequireGrant>
    );
  }
  if (path === "/settings/kubernetes") {
    return (
      <RequireGrant permission="feature.manage">
        <KubernetesPage />
      </RequireGrant>
    );
  }
  if (path === "/store") {
    return <StorePage />;
  }
  if (path === "/automation") {
    return <AutomationHome />;
  }
  if (path === "/automation/policies") {
    return <AutomationPage />;
  }
  if (/^\/automation\/flows\/[^/]+$/.test(path)) {
    return <FlowEditorPage key={path} />;
  }
  if (/^\/automation\/runs\/[^/]+$/.test(path)) {
    return <RunPage key={path} />;
  }
  if (path === "/ask") {
    return <AskPage />;
  }
  if (path === "/plans") {
    return <PlansPage />;
  }
  if (path === "/settings/license") {
    return (
      <RequireGrant permission="settings.license.manage">
        <LicensePage />
      </RequireGrant>
    );
  }
  if (path === "/docs") {
    return <DocsPage />;
  }
  if (path === "/settings/certificates") {
    return (
      <RequireGrant permission="settings.tls.manage">
        <CertificatePage />
      </RequireGrant>
    );
  }
  if (path === "/settings/updates") {
    return (
      <RequireGrant permission="updates.manage">
        <UpdatesPage />
      </RequireGrant>
    );
  }
  if (path === "/settings/security" || path === "/roles/assignments") {
    // Security moved to the Users page (MFA policy); Assignments live on Users.
    return <Redirect to="/users" />;
  }
  if (path === "/settings/mfa") {
    // MFA moved to Account → Security; the old address still lands there.
    return <Redirect to="/me?tab=security" />;
  }
  if (path === "/groups" || path === "/roles" || path === "/roles/permissions") {
    return <Redirect to="/iam/access" />;
  }
  if (path === "/iam/access") {
    return (
      <IAMLayout>
        <AccessControlArea view="roles" />
      </IAMLayout>
    );
  }
  if (path === "/iam/approvals") {
    return (
      <IAMLayout>
        <RequireGrant permission="approvals.view">
          <ApprovalsPage />
        </RequireGrant>
      </IAMLayout>
    );
  }
  if (path === "/notifications") {
    return <NotificationsPage />;
  }
  if (path === "/audit") {
    // Without audit.read the page shows tasks only.
    return <AuditPage />;
  }
  if (path === "/iam") {
    return <IAMHome />;
  }
  if (path === "/users") {
    return (
      <IAMLayout>
        <RequireGrant permission="users.read">
          <UsersPage />
        </RequireGrant>
      </IAMLayout>
    );
  }
  if (path === "/backups") {
    return <BackupsPage />;
  }
  if (path === "/api-access") {
    // API access lives under Access control; its address is kept for bookmarks.
    return (
      <IAMLayout>
        <AccessControlArea view="api" />
      </IAMLayout>
    );
  }
  if (path === "/") {
    return <DashboardPage />;
  }
  return <NotFoundPage />;
}

/** Workload pages carry the workload's own tab bar. */
function withFrame(path: string, page: ReactNode): ReactNode {
  const selected = selectedTargetFromPath(path);
  if (selected?.kind !== "workload" || isGameServersContext(path)) {
    return page;
  }
  return <WorkloadFrame id={selected.id}>{page}</WorkloadFrame>;
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
          <ErrorNotice error={session.message} />
          <button className="btn btn-primary" type="button" onClick={() => void session.refresh()}>
            Try again
          </button>
        </main>
      </div>
    );
  }

  if (path === "/setup/recover") {
    // The page checks for itself whether recovery is still possible, and
    // stays up through the restart that ends it.
    return <RecoverPage />;
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
    <NavDisclosureProvider>
      <Shell>
        <TerminalWorkspaceProvider>
          <AreaFrame>{withFrame(path, <FeatureGate path={path}>{matchPage(path)}</FeatureGate>)}</AreaFrame>
        </TerminalWorkspaceProvider>
      </Shell>
    </NavDisclosureProvider>
  );
}

/** PrefsBinder loads the signed-in user's saved interface preferences. */
function PrefsBinder() {
  const session = useSession();
  const user = session.status === "ready" ? session.user : null;
  const uid = user?.user_id ?? "";
  useEffect(() => {
    if (session.status !== "ready") {
      return;
    }
    if (user) {
      bindPrefsUser(user.user_id, user.ui_prefs);
    } else {
      unbindPrefs();
    }
    // Rebinding on every profile change would reset unsaved preferences.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.status, uid]);
  return null;
}

/** AppearanceBinder loads the theme and layouts an admin set for this person. */
function AppearanceBinder() {
  const session = useSession();
  const uid = session.status === "ready" ? (session.user?.user_id ?? "") : "";
  useEffect(() => {
    if (uid) {
      void loadAppearanceDefaults();
    } else {
      resetAppearanceDefaults();
    }
  }, [uid]);
  return null;
}

export function App() {
  return (
    <SessionProvider>
      <PrefsBinder />
      <AppearanceBinder />
      <GlobalContextMenu />
      <AppRoutes />
    </SessionProvider>
  );
}
