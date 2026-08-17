package session

import "testing"

func TestSearchConfigClampParams(t *testing.T) {
	cases := []struct {
		name string
		in   SearchConfig
		want SearchConfig
	}{
		{"zero → defaults", SearchConfig{}, SearchConfig{FetchTopK: DefaultSearchFetchTopK, PageChars: DefaultSearchPageChars}},
		{"above max → clamped", SearchConfig{FetchTopK: 100, PageChars: 999999}, SearchConfig{FetchTopK: 10, PageChars: 20000}},
		{"below min / negative", SearchConfig{FetchTopK: -1, PageChars: 500}, SearchConfig{FetchTopK: DefaultSearchFetchTopK, PageChars: 1000}},
		{"in range unchanged", SearchConfig{FetchTopK: 3, PageChars: 8000}, SearchConfig{FetchTopK: 3, PageChars: 8000}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.in
			got.clampParams()
			if got.FetchTopK != c.want.FetchTopK || got.PageChars != c.want.PageChars {
				t.Errorf("clampParams(%+v) = {%d,%d}, want {%d,%d}",
					c.in, got.FetchTopK, got.PageChars,
					c.want.FetchTopK, c.want.PageChars)
			}
		})
	}
}
