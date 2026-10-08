package relay

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/privacy"
)

// Capture per-round credentials before constructing the outbound request.
// Diagnostic redaction must still work after the channel/key is edited or removed.
func rememberChannelSecrets(redactor *privacy.Redactor, channel model.Channel, key string, headers http.Header) {
	redactor.Register(privacy.KnownCredentials, key)
	redactor.AddURL(channel.BaseURL)
	redactor.AddURL(channel.ChannelProxy)
	redactor.AddURL(channel.OpenAIChatCompletionPath)
	redactor.AddURL(channel.OpenAIResponsePath)
	redactor.AddURL(channel.AnthropicMessagePath)
	if channel.Proxy && channel.ChannelProxy == "" {
		if proxy, err := op.SettingGetString(model.SettingKeyProxyURL); err == nil {
			redactor.AddURL(proxy)
		}
	}
	for _, header := range channel.CustomHeader {
		value := clientHeaderPlaceholder.ReplaceAllStringFunc(header.HeaderValue, func(placeholder string) string {
			return headers.Get(placeholder[len("{client_header:") : len(placeholder)-1])
		})
		redactor.Register(privacy.KnownCredentials, value)
	}
	// Parameter overrides can contain credential fields that are later echoed.
	redactor.Body(channel.ParamOverride)
}
