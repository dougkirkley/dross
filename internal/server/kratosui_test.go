package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/internal/auth"
)

// A real login flow, as Kratos answered one from the running server. Kept
// verbatim because the point of this test is that dross draws what Kratos
// actually sends, not what its documentation implies.
const loginFlowJSON = `{
  "id": "07fb6594-a877-4508-8828-d8022d5d83af",
  "type": "browser",
  "ui": {
    "action": "https://dross.sh/kratos/self-service/login?flow=07fb6594-a877-4508-8828-d8022d5d83af",
    "method": "POST",
    "nodes": [
      {"type":"input","group":"oidc","attributes":{"name":"provider","type":"submit","value":"access","disabled":false},
       "meta":{"label":{"text":"Sign in with access"}}},
      {"type":"input","group":"default","attributes":{"name":"csrf_token","type":"hidden","value":"jrHY8D8uqk73H6Ag","required":true,"disabled":false},"meta":{}}
    ]
  }
}`

func TestFlowViewDrawsWhatKratosSent(t *testing.T) {
	var f auth.UIFlow
	if err := json.Unmarshal([]byte(loginFlowJSON), &f); err != nil {
		t.Fatal(err)
	}
	v := flowView(&f, "Sign in", "Dross", "Sign in to continue.", "foot")

	if v.Method != "post" || !strings.HasPrefix(v.Action, "https://dross.sh/kratos/self-service/login?flow=") {
		t.Fatalf("form posts to %q %q", v.Method, v.Action)
	}
	// The CSRF token has to survive into the form, or every submit is rejected
	// and the failure looks like a login problem rather than a rendering one.
	if len(v.Hidden) != 1 || v.Hidden[0].Name != "csrf_token" || v.Hidden[0].Value == "" {
		t.Fatalf("csrf token did not make it into the form: %+v", v.Hidden)
	}
	if len(v.Buttons) != 1 {
		t.Fatalf("buttons = %+v; want one", v.Buttons)
	}
	b := v.Buttons[0]
	// Kratos decides what happened from which button was submitted, so the name
	// and value are load-bearing, not decoration.
	if b.Name != "provider" || b.Value != "access" {
		t.Errorf("button submits %s=%s; Kratos will not know which provider", b.Name, b.Value)
	}
	// And it is labelled for the person reading it, not after the config's id.
	if b.Label != "Continue with Google" {
		t.Errorf("button label = %q; want the provider the user recognises", b.Label)
	}
}

// A disabled node is Kratos saying "not available"; drawing it anyway offers
// something that cannot work.
func TestFlowViewSkipsDisabledNodes(t *testing.T) {
	var f auth.UIFlow
	if err := json.Unmarshal([]byte(strings.Replace(loginFlowJSON, `"disabled":false},
       "meta":{"label":{"text":"Sign in with access"}}}`, `"disabled":true},
       "meta":{"label":{"text":"Sign in with access"}}}`, 1)), &f); err != nil {
		t.Fatal(err)
	}
	v := flowView(&f, "Sign in", "Dross", "", "")
	if len(v.Buttons) != 0 {
		t.Errorf("drew %d disabled buttons; want none", len(v.Buttons))
	}
}

// The flow page hands off to an identity provider, so it must not carry a
// form-action directive. Chrome applies that to every hop of the redirect chain
// a submission follows, and the hop after ours belongs to the provider - so the
// policy blocks the handoff and blames the form. Every other page dross renders
// posts only to itself and keeps the directive.
func TestFlowPageDoesNotBlockItsOwnHandoff(t *testing.T) {
	w := httptest.NewRecorder()
	writeFlowPage(w, http.StatusOK)
	csp := w.Header().Get("Content-Security-Policy")

	if strings.Contains(csp, "form-action") {
		t.Errorf("the flow page carries form-action, which blocks the sign-in it exists to start: %q", csp)
	}
	// The rest of the lockdown has to survive: dropping one directive is not a
	// reason to stop denying everything else.
	for _, want := range []string{"default-src 'none'", "style-src 'unsafe-inline'", "img-src data:"} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy lost %q: %q", want, csp)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("a page carrying a CSRF token must not be cached: %q", w.Header().Get("Cache-Control"))
	}
}
