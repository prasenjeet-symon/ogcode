/* @refresh reload */
import { render } from 'solid-js/web';
import App from './app';
import './styles/index.css';
import { bootstrapAnalytics } from './lib/posthog';

const root = document.getElementById('root');
if (root) {
  render(() => <App />, root);
}

// Fire-and-forget: initialise PostHog analytics (always-on, hardcoded credentials),
// adopting the install id the server reports so the funnel is one person.
// This is intentionally non-blocking — analytics should never delay app render.
bootstrapAnalytics();