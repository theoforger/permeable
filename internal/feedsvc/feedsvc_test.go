package feedsvc

import (
	"testing"

	"permeable/internal/model"
)

func TestEffectiveTTLMinutes(t *testing.T) {
	ptr := func(i int) *int { return &i }

	cases := []struct {
		name    string
		sources []model.Source
		maxTTL  int
		want    int
	}{
		{
			name:    "no source declares TTL uses max alone",
			sources: []model.Source{{Enabled: true, SourceTTLMinutes: nil}},
			maxTTL:  60,
			want:    60,
		},
		{
			name: "single declared TTL under max wins",
			sources: []model.Source{
				{Enabled: true, SourceTTLMinutes: ptr(15)},
			},
			maxTTL: 60,
			want:   15,
		},
		{
			name: "declared TTL over max is capped by max",
			sources: []model.Source{
				{Enabled: true, SourceTTLMinutes: ptr(120)},
			},
			maxTTL: 60,
			want:   60,
		},
		{
			name: "most frequent declared value wins over a lone outlier",
			sources: []model.Source{
				{Enabled: true, SourceTTLMinutes: ptr(30)},
				{Enabled: true, SourceTTLMinutes: ptr(30)},
				{Enabled: true, SourceTTLMinutes: ptr(15)},
			},
			maxTTL: 60,
			want:   30,
		},
		{
			name: "tie breaks toward the smaller value",
			sources: []model.Source{
				{Enabled: true, SourceTTLMinutes: ptr(30)},
				{Enabled: true, SourceTTLMinutes: ptr(15)},
			},
			maxTTL: 60,
			want:   15,
		},
		{
			name: "disabled sources are ignored",
			sources: []model.Source{
				{Enabled: false, SourceTTLMinutes: ptr(5)},
				{Enabled: true, SourceTTLMinutes: ptr(20)},
			},
			maxTTL: 60,
			want:   20,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveTTLMinutes(tc.sources, tc.maxTTL)
			if got != tc.want {
				t.Errorf("effectiveTTLMinutes() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestApplyTitlePrefix(t *testing.T) {
	sources := map[int64]model.Source{
		1: {ID: 1, TitlePrefix: "[Work]"},
		2: {ID: 2, TitlePrefix: ""},
	}

	prefixed := applyTitlePrefix(model.Event{SourceID: 1, Title: "Standup"}, sources)
	if prefixed.Title != "[Work] Standup" {
		t.Errorf("Title = %q, want %q", prefixed.Title, "[Work] Standup")
	}

	unprefixed := applyTitlePrefix(model.Event{SourceID: 2, Title: "Standup"}, sources)
	if unprefixed.Title != "Standup" {
		t.Errorf("Title = %q, want unchanged %q", unprefixed.Title, "Standup")
	}
}
