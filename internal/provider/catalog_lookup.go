package provider

import (
	"regexp"
	"strings"
	"sync"
)

// The same model reaches ogcode under many names. Claude Sonnet 4.5 is
// "claude-sonnet-4-5-20250929" on Anthropic's API, "anthropic/claude-sonnet-4.5"
// on OpenRouter; gpt-oss-20b is "openai/gpt-oss-20b" on Groq and "gpt-oss:20b" on
// Ollama; a model a user adds by hand can be spelled any of these ways. The
// catalogue is only useful if every spelling finds the same entry, so lookups
// go through normalizeModelID, and each entry lists the spellings (Aliases) that
// normalisation alone cannot reach — a size tag, a host-specific name.

// openRouterVariants are OpenRouter's ":suffix" routing variants. They pick a
// price tier or endpoint for the same model, so they are stripped before lookup.
var openRouterVariants = map[string]bool{
	"free": true, "beta": true, "nitro": true, "floor": true, "online": true,
	"extended": true, "thinking": true, "exacto": true, "batch": true,
}

var dashRun = regexp.MustCompile(`-{2,}`)

// normalizeModelID reduces a model id to the form the catalogue index is keyed
// on: lower case, without an org/host prefix ("anthropic/", "meta-llama/"),
// without Ollama's ":latest" and cloud tags or OpenRouter's routing variants,
// and with ".", ":", "_" and spaces folded to "-" — so "claude-sonnet-4.5" and
// "claude-sonnet-4-5", or "gpt-oss:20b" and "gpt-oss-20b", meet.
func normalizeModelID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ":latest")
	// Ollama cloud tags: "glm-4.6:cloud", "qwen3-coder:480b-cloud".
	s = strings.TrimSuffix(s, ":cloud")
	s = strings.TrimSuffix(s, "-cloud")
	if i := strings.LastIndex(s, ":"); i >= 0 && openRouterVariants[s[i+1:]] {
		s = s[:i]
	}
	s = strings.NewReplacer(".", "-", ":", "-", "_", "-", " ", "-").Replace(s)
	s = dashRun.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// dateSuffix matches a trailing snapshot date — "-20250929" or "-2025-08-07"
// (after normalisation both are dash-separated digits). Stripping it lets an
// undated catalogue entry answer for a dated snapshot it does not list.
var dateSuffix = regexp.MustCompile(`-(20\d{6}|20\d{2}-\d{2}-\d{2})$`)

// catalogIndex maps every normalised id and alias to its entry.
type catalogIndex map[string]CatalogModel

// newCatalogIndex indexes lists in order; the first entry to claim a key keeps
// it, so earlier lists (first-party catalogues) take precedence.
func newCatalogIndex(lists ...[]CatalogModel) catalogIndex {
	idx := catalogIndex{}
	for _, list := range lists {
		for _, m := range list {
			for _, key := range append([]string{m.ID}, m.Aliases...) {
				k := normalizeModelID(key)
				if k == "" {
					continue
				}
				if _, taken := idx[k]; !taken {
					idx[k] = m
				}
			}
		}
	}
	return idx
}

// lookup finds id by its normalised form, then without a snapshot date suffix.
func (idx catalogIndex) lookup(id string) (CatalogModel, bool) {
	k := normalizeModelID(id)
	if k == "" {
		return CatalogModel{}, false
	}
	if m, ok := idx[k]; ok {
		return m, true
	}
	if base := dateSuffix.ReplaceAllString(k, ""); base != k {
		if m, ok := idx[base]; ok {
			return m, true
		}
	}
	return CatalogModel{}, false
}

var (
	catalogOnce sync.Once
	catalogIdx  catalogIndex
)

// LookupCatalogModel finds a model in the built-in catalogue under any of its
// names, whichever provider serves it. The catalogue is the authority on a
// known model's context window, pricing and image support; a false return means
// "not catalogued" — unknown, never free or zero.
func LookupCatalogModel(id string) (CatalogModel, bool) {
	catalogOnce.Do(func() {
		catalogIdx = newCatalogIndex(AnthropicModels, OpenAIModels, OpenModels, LegacyModels)
	})
	return catalogIdx.lookup(id)
}
