import type { ModelUsage } from '../api/client';
import { PROVIDER_DEFS } from './providers';

// Dollar amounts as the cost views show them. Model spend runs from fractions
// of a cent per call to dollars a month, so precision follows the size: cents
// from a dollar up, and below it either cents (compact, for the pill and tiles)
// or four places (precise, for breakdowns). Real spend never reads as free —
// anything under the smallest step shows as "<".
export function formatUSD(n: number, precise = false): string {
  if (!Number.isFinite(n) || n <= 0) return '$0.00';
  if (n >= 1000) return '$' + Math.round(n).toLocaleString('en-US');
  if (n >= 1) return '$' + n.toFixed(2);
  if (precise) return n < 0.0001 ? '<$0.0001' : '$' + n.toFixed(4);
  return n < 0.01 ? '<$0.01' : '$' + n.toFixed(2);
}

/** A provider id as people know it. */
export function providerLabel(id: string): string {
  if (id === 'ogx') return 'OGX';
  return PROVIDER_DEFS.find((p) => p.id === id)?.label ?? (id || 'Unknown');
}

/** Why an included model costs nothing per token. */
export function includedNote(m: Pick<ModelUsage, 'provider' | 'model' | 'inferredProvider'>): string {
  const provider = m.provider || m.inferredProvider || '';
  if (provider === 'ogx') return 'Included in your OGX plan';
  // Ollama serves both: cloud models (tagged :cloud / -cloud) on the Ollama
  // plan, everything else on this machine.
  if (provider === 'ollama' && /[:-]cloud$/i.test(m.model)) return 'Included in your Ollama plan';
  return 'Runs locally, no charge';
}

/**
 * The provider a usage row is attributed to, as a short tag: the recorded one,
 * one inferred for work from before providers were recorded, or neither. A
 * slot pointed away from its own default endpoint is named by the host it
 * calls — the OpenAI slot at Z.ai reads "api.z.ai", not "OpenAI".
 */
export function providerTag(m: Pick<ModelUsage, 'provider' | 'inferredProvider' | 'host'>): string {
  const name = (id: string) => m.host || providerLabel(id);
  if (m.provider) return name(m.provider);
  if (m.inferredProvider) return `${name(m.inferredProvider)} · inferred`;
  return 'Not recorded';
}

/** The tag's tooltip: which slot a host-named row went through. */
export function providerTitle(m: Pick<ModelUsage, 'provider' | 'inferredProvider' | 'host'>): string | undefined {
  const id = m.provider || m.inferredProvider;
  if (!id) return 'The provider was not recorded for this work, and nothing configured serves the model now';
  const slot = `${providerLabel(id)} slot`;
  const how = m.provider ? '' : ' (inferred: it serves this model now)';
  return m.host ? `${slot} pointed at ${m.host}${how}` : `${slot}${how}`;
}
