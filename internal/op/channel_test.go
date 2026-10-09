package op

import (
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestNormalizeChannelConfigValidatesTrimmedMatchRegex(t *testing.T) {
	config := model.ChannelConfig{
		Name:       "test channel",
		BaseURL:    "https://example.com",
		MatchRegex: " *gpt",
	}

	_, err := normalizeChannelConfig(config)
	if err == nil {
		t.Fatal("expected regex to be rejected after trimming")
	}
}

func TestNormalizeChannelConfigAcceptsEmptyAndValidMatchRegex(t *testing.T) {
	for _, regex := range []string{"", "   ", "^gpt-"} {
		t.Run(regex, func(t *testing.T) {
			config := model.ChannelConfig{
				Name:       "test channel",
				BaseURL:    "https://example.com",
				MatchRegex: regex,
			}

			normalized, err := normalizeChannelConfig(config)
			if err != nil {
				t.Fatalf("normalizeChannelConfig() error = %v", err)
			}
			if normalized.MatchRegex != regex && !(regex == "   " && normalized.MatchRegex == "") {
				t.Fatalf("MatchRegex = %q, want normalized %q", normalized.MatchRegex, regex)
			}
		})
	}
}
