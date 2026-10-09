package relay

import (
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestBuildOutboundUsesEffectiveChannelProtocols(t *testing.T) {
	protocols := []model.Protocol{
		model.ProtocolOpenAIChatCompletion,
		model.ProtocolOpenAIResponse,
		model.ProtocolAnthropicMessage,
	}
	for _, protocol := range protocols {
		t.Run(protocolName(protocol), func(t *testing.T) {
			channel := model.Channel{ChannelConfig: model.ChannelConfig{
				BaseURL:                  "https://upstream.example.com",
				AllowedProtocols:         protocol,
				OpenAIChatCompletionPath: "/v1/chat/completions",
				OpenAIResponsePath:       "/v1/responses",
				AnthropicMessagePath:     "/v1/messages",
			}}
			grant := model.ChannelGrant{ID: 1, Protocols: model.ProtocolAll}
			key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "secret"}}
			_, selected, passthrough, err := buildOutbound(channel, grant, key, protocol)
			if err != nil {
				t.Fatal(err)
			}
			if selected != protocol || !passthrough {
				t.Fatalf("selected protocol=%d passthrough=%v, want %d/true", selected, passthrough, protocol)
			}
		})
	}
}

func protocolName(protocol model.Protocol) string {
	switch protocol {
	case model.ProtocolOpenAIChatCompletion:
		return "openai-chat"
	case model.ProtocolOpenAIResponse:
		return "openai-response"
	case model.ProtocolAnthropicMessage:
		return "anthropic-message"
	default:
		return "unknown"
	}
}
