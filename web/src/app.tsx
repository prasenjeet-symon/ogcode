import { Navigate, Route, Router, useNavigate, useLocation } from '@solidjs/router';
import { createEffect, onCleanup } from 'solid-js';
import { closeDrawer } from './lib/mobile-drawer';
import { ServerProvider } from './context/server';
import { OnboardingProvider, useOnboarding } from './context/onboarding';
import { SessionProvider } from './context/session';
import { PlanProvider } from './context/plan';
import { NoteProvider } from './context/note';
import { DocIndexProvider } from './context/docindex';
import { NotificationProvider } from './context/notification';
import { DesktopNotificationProvider } from './context/desktop-notification';
import { ThemeProvider } from './context/theme';
import UpdateNotification from './components/update-notification';
import GitSyncBanner from './components/git-sync-banner';
import DesktopNotificationBanner from './components/desktop-notification-banner';
import ModelSwitchPopup from './components/model-switch-popup';
import Home from './pages/home';
import Chat from './pages/session';
import PlanList from './pages/plan-list';
import PlanDetail from './pages/plan-detail';
import PlanTasksPage from './pages/plan-tasks';
import TaskExecution from './pages/task-execution';
import NotesPage from './pages/notes';
import NoteDetailPage from './pages/note-detail';
import DocIndexPage from './pages/docindex';
import SettingsLayout from './pages/settings/layout';
import GeneralSettings from './pages/settings/general';
import ModelsSettings from './pages/settings/models';
import SkillsSettings from './pages/settings/skills';
import MCPSettings from './pages/settings/mcp';
import AboutSettings from './pages/settings/about';
import Onboarding from './pages/onboarding';
import NotFound from './pages/not-found';

export default function App() {
  return (
    <Router root={AppWrapper}>
      <Route path="/onboarding" component={Onboarding} />
      <Route path="/" component={Home} />
      <Route path="/session/:id" component={Chat} />
      <Route path="/plan" component={PlanList} />
      <Route path="/plan/:id" component={PlanDetail} />
      <Route path="/plan/:id/tasks" component={PlanTasksPage} />
      <Route path="/task/:id" component={TaskExecution} />
      <Route path="/notes" component={NotesPage} />
      <Route path="/notes/:id" component={NoteDetailPage} />
      <Route path="/docindex" component={DocIndexPage} />
      {/* Skills moved under Settings, where the rest of "what this agent can
          reach for" lives. The old path still resolves so bookmarks and links
          from earlier sessions do not dead-end on the 404 page. */}
      <Route path="/skills" component={() => <Navigate href="/settings/skills" />} />
      <Route path="/settings" component={SettingsLayout}>
        <Route path="/" component={GeneralSettings} />
        <Route path="/models" component={ModelsSettings} />
        <Route path="/skills" component={SkillsSettings} />
        <Route path="/mcp" component={MCPSettings} />
        <Route path="/about" component={AboutSettings} />
      </Route>
      {/* Catch-all: without it the router matches nothing and renders a blank
          page, leaving no way back from a typo'd or stale URL. */}
      <Route path="*404" component={NotFound} />
    </Router>
  );
}

function AppWrapper(props: { children?: any }) {
  return (
    <ServerProvider>
      <OnboardingProvider>
        <ThemeProvider>
          <SessionProvider>
            <PlanProvider>
              <NoteProvider>
                  <DocIndexProvider>
                    <NotificationProvider>
                      <DesktopNotificationProvider>
                        <OnboardingGate />
                        {/* Close any open mobile drawer whenever the route
                            changes. Lives here — not in SidebarShell — because
                            every page renders its own shell: the shell that
                            saw the navigation unmounts mid-route, and the next
                            page's shell initializes lastPath AFTER the route
                            changed, so it would never see a "navigation".
                            AppWrapper mounts once and observes every change. */}
                        <DrawerNavCloser />
                        <div class="flex h-dvh bg-[color:var(--bg-base)] text-zinc-100 antialiased">
                          {props.children}
                        </div>
                        <UpdateNotification />
                        <GitSyncBanner />
                        <DesktopNotificationBanner />
                        <ModelSwitchPopup />
                      </DesktopNotificationProvider>
                    </NotificationProvider>
                  </DocIndexProvider>
              </NoteProvider>
            </PlanProvider>
          </SessionProvider>
        </ThemeProvider>
      </OnboardingProvider>
    </ServerProvider>
  );
}

// OnboardingGate redirects first-run users (no provider configured) to the
// onboarding wizard. It renders nothing; it only runs the redirect effect.
function OnboardingGate() {
  const onboarding = useOnboarding();
  const navigate = useNavigate();
  const location = useLocation();
  createEffect(() => {
    if (!onboarding.loaded()) return;
    if (onboarding.dismissed()) return;
    if (onboarding.needsOnboarding() && location.pathname !== '/onboarding') {
      navigate('/onboarding', { replace: true });
    }
  });
  return null;
}

// Closes the mobile navigation drawer on any route change. Rendered once in
// AppWrapper (inside the Router, so useLocation works) and never unmounts.
// Tracks the pathname across runs; on the very first run it just records the
// current path — there is nothing to close yet, and treating mount as
// navigation would close a drawer the user just opened.
function DrawerNavCloser() {
  const location = useLocation();
  let lastPath = location.pathname;
  createEffect(() => {
    const p = location.pathname;
    if (p !== lastPath) closeDrawer();
    lastPath = p;
  });
  onCleanup(() => { closeDrawer(); });
  return null;
}