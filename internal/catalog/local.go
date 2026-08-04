package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// What a local server can be asked besides inference.
//
// Inference is identical across these — all four speak the OpenAI-compatible
// route — so this is not a second backend. It is the small set of questions
// that only some of them can answer, kept in one place instead of spread
// through whichever caller happened to need one.

// ModelInfo is what a local server reports about a model it holds.
type ModelInfo struct {
	// Tools reports whether the model's template declares tool calling.
	//
	// Worth stating plainly: this is what the template claims, not what the
	// model reliably does. qwen2.5-coder:14b declares tools here and, asked to
	// use one, returns no structured call and writes JSON into its prose
	// instead. So a false is trustworthy — gemma2 correctly declares none, and
	// a Code session on it cannot work — while a true is only a licence to try.
	Tools    bool
	Thinking bool
	// Params and Quant describe the weights ("20.9B", "MXFP4"), which is what
	// decides whether a model fits a card.
	Params string
	Quant  string
	// Context is the model's own maximum, which the server may be running
	// below.
	Context int
	// Known is false when the server could not be asked, so that "no tools"
	// and "we do not know" are never confused.
	Known bool
}

// Describe asks a local server what it knows about a model.
//
// Only ollama can answer; the others have no such route, and saying so is
// better than guessing. A hosted provider is never asked at all.
func (p Provider) Describe(ctx context.Context, model string) ModelInfo {
	if p.Local != Ollama || model == "" {
		return ModelInfo{}
	}
	root := ollamaRoot(p.BaseURL)
	if root == "" {
		return ModelInfo{}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root+"/api/show", bytes.NewReader(body))
	if err != nil {
		return ModelInfo{}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return ModelInfo{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ModelInfo{}
	}
	var out struct {
		Capabilities []string `json:"capabilities"`
		Details      struct {
			ParameterSize     string `json:"parameter_size"`
			QuantizationLevel string `json:"quantization_level"`
		} `json:"details"`
		ModelInfo map[string]any `json:"model_info"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ModelInfo{}
	}
	info := ModelInfo{
		Known:  true,
		Params: out.Details.ParameterSize,
		Quant:  out.Details.QuantizationLevel,
	}
	for _, c := range out.Capabilities {
		switch c {
		case "tools":
			info.Tools = true
		case "thinking":
			info.Thinking = true
		}
	}
	// The context length key is namespaced by architecture
	// ("llama.context_length", "gptoss.context_length", …), so it is found by
	// suffix rather than by a name that changes with every model family.
	for k, v := range out.ModelInfo {
		if !strings.HasSuffix(k, ".context_length") {
			continue
		}
		if n, ok := v.(float64); ok && int(n) > info.Context {
			info.Context = int(n)
		}
	}
	return info
}

// ollamaRoot turns an OpenAI-compatible base URL into ollama's own API root,
// which sits one level up from /v1.
func ollamaRoot(baseURL string) string {
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return strings.TrimSuffix(b, "/v1")
}
