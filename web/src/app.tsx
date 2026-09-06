import { Navigate, Route, Router, useNavigate, useLocation } from '@solidjs/router';
import { createEffect } from 'solid-js';
import { ServerProvider } from './context/server';
import { OnboardingProvider, useOnboarding } from './context/onboarding';
import { SessionProvider } from './context/session';
import { PlanProvider } from './context/plan';
import { NoteProvider } from './context/note';
import { DocIndexProvider } from './context/docindex';
import { NotificationProvider } from './context/notification';
import { ThemeProvider } from './context/theme';
import UpdateNotification from './components/update-notification';
import GitSyncBanner from './components/git-sync-banner';
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
                      <OnboardingGate />
                      <div class="flex h-dvh bg-[color:var(--bg-base)] text-zinc-100 antialiased">
                        {props.children}
                      </div>
                      <UpdateNotification />
                      <GitSyncBanner />
                      <ModelSwitchPopup />
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