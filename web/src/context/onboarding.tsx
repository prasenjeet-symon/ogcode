import { createContext, useContext, createSignal, type ParentComponent } from 'solid-js';
import { getOllamaStatus, type OllamaStatus } from '../api/client';

interface OnboardingContextValue {
  // True once the initial provider-config check has completed.
  loaded: () => boolean;
  // True when no LLM provider is configured (neither via env var nor saved to
  // the DB) — i.e. the user should be sent through the onboarding wizard.
  needsOnboarding: () => boolean;
  // True when the user chose to skip onboarding for this session. The redirect
  // gate honours this so "Skip for now" doesn't bounce straight back.
  dismissed: () => boolean;
  // Mark onboarding as skipped for this session.
  dismiss: () => void;
  // Re-run the check (e.g. after the wizard saves credentials).
  refresh: () => Promise<void>;
  // Ollama runtime detection state (installed/running). Null before the
  // first check completes.
  ollamaStatus: () => OllamaStatus | null;
}

const OnboardingContext = createContext<OnboardingContextValue>();

// Onboarding is disabled for new users: the community free-tier key pool makes
// the app usable out of the box, so nobody is forced through the setup wizard.
// `needsOnboarding` is therefore always false. The /onboarding page itself
// stays reachable manually (e.g. from Settings) for users who want to add their
// own provider keys.

export const OnboardingProvider: ParentComponent = (props) => {
  const [loaded, setLoaded] = createSignal(false);
  const [needsOnboarding, setNeedsOnboarding] = createSignal(false);
  const [dismissed, setDismissed] = createSignal(false);
  const [ollamaStatus, setOllamaStatus] = createSignal<OllamaStatus | null>(null);

  const refresh = async () => {
    try {
      // Onboarding is disabled — new users get working free models out of the
      // box, so they are never forced into the setup wizard. We still probe
      // Ollama so the (manually reachable) onboarding page can show its status.
      const ollama = await getOllamaStatus().catch(() => null);
      setOllamaStatus(ollama);
      setNeedsOnboarding(false);
    } finally {
      setLoaded(true);
    }
  };

  const dismiss = () => setDismissed(true);

  void refresh();

  const value: OnboardingContextValue = { loaded, needsOnboarding, dismissed, dismiss, refresh, ollamaStatus };
  return (
    <OnboardingContext.Provider value={value}>
      {props.children}
    </OnboardingContext.Provider>
  );
};

export function useOnboarding() {
  const ctx = useContext(OnboardingContext);
  if (!ctx) throw new Error('useOnboarding must be used within OnboardingProvider');
  return ctx;
}