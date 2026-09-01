package gateway

import "net/http"

const (
	headerRequestID       = "X-LlamaRack-Request-ID"
	headerTraceID         = "X-LiteLLM-Trace-ID"
	headerInstance        = "X-LlamaRack-Instance"
	headerAutoloaded      = "X-LlamaRack-Autoloaded"
	headerQueueMS         = "X-LlamaRack-Queue-MS"
	headerLoadMS          = "X-LlamaRack-Load-MS"
	headerTTFTMS          = "X-LlamaRack-TTFT-MS"
	headerPromptTPS       = "X-LlamaRack-Prompt-Tokens-Per-Second"
	headerGenerationTPS   = "X-LlamaRack-Generation-Tokens-Per-Second"
	headerPromptTokens    = "X-LlamaRack-Prompt-Tokens"
	headerGeneratedTokens = "X-LlamaRack-Generated-Tokens"
	headerTotalTokens     = "X-LlamaRack-Total-Tokens"
	headerUpstreamPort    = "X-LlamaRack-Upstream-Port"
)

func setProductHeader(header http.Header, name, value string) {
	header.Set(name, value)
}

func getProductHeader(header http.Header, name string) string {
	return header.Get(name)
}

func CORSExposeHeaders() string {
	return "X-LlamaRack-Request-ID, X-LiteLLM-Trace-ID, X-LiteLLM-Session-ID, X-LlamaRack-Instance, X-LlamaRack-Autoloaded, X-LlamaRack-Upstream-Port, X-LlamaRack-Queue-MS, X-LlamaRack-Load-MS, X-LlamaRack-TTFT-MS, X-LlamaRack-Prompt-Tokens-Per-Second, X-LlamaRack-Generation-Tokens-Per-Second, X-LlamaRack-Prompt-Tokens, X-LlamaRack-Generated-Tokens, X-LlamaRack-Total-Tokens"
}
