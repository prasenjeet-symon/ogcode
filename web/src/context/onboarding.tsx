import { createContext, useContext, createSignal, type ParentComponent } from 'solid-js';
import { getProviderConfigs, getOllamaStatus, type ProviderConfig, type OllamaStatus } from '../api/client';

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

// A provider counts as "configured" when its key is stored in the DB (apiKey
// sentinel "__SET__") or supplied via environment variable. Keyless local
// providers (Ollama) instead count as configured once a base URL is set —
// otherwise completing the wizard with Ollama would leave the app looking
// unconfigured and bounce the user back into onboarding.
//
// Additionally, a *running* local Ollama instance (detected via the backend
// health probe) counts as configured so the user skips the API-key wizard
// entirely (zero-config flow).
function isConfigured(configs: ProviderConfig[], ollama: OllamaStatus | null): boolean {
  if (configs.some(
    (c) =>
      c.apiKey === '__SET__' ||
      c.envKeySet ||
      (c.providerId === 'ollama' && (c.baseUrl !== '' || c.envBaseURLSet)),
  )) {
    return true;
  }
  // A running Ollama server satisfies the gate even with no saved config.
  return !!ollama?.running;
}

export const OnboardingProvider: ParentComponent = (props) => {
  const [loaded, setLoaded] = createSignal(false);
  const [needsOnboarding, setNeedsOnboarding] = createSignal(false);
  const [dismissed, setDismissed] = createSignal(false);
  const [ollamaStatus, setOllamaStatus] = createSignal<OllamaStatus | null>(null);

  const refresh = async () => {
    try {
      const [configs, ollama] = await Promise.all([
        getProviderConfigs(),
        getOllamaStatus().catch(() => null),
      ]);
      setOllamaStatus(ollama);
      setNeedsOnboarding(!isConfigured(configs, ollama));
    } catch {
      // Fail open: if the check errors, never trap the user in onboarding.
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