package server

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/dougkirkley/dross/internal/auth"
	"github.com/dougkirkley/dross/internal/ui"
)

// The browser's half of Kratos: a proxy so its API is same-origin, and the
// pages it points at.
//
// Same-origin is not a preference. A session cookie only goes back to the host
// that set it, so identity served from a second hostname would issue cookies
// dross never receives, and every request would arrive anonymous. Kratos is
// therefore reachable at dross.sh/kratos/, and its own base_url says so.
//
// Kratos renders no UI at all. It answers with a flow — an id, where to post,
// which fields to include, what the buttons are called — and expects an
// application to draw it. That is what these handlers do, and why they draw
// what the flow says rather than what the config said when this was written.

// kratosProxy forwards /kratos/* to the public API, stripping the prefix.
func (s *Server) kratosProxy(target *url.URL) http.Handler {
	p := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, "/kratos")
			if r.Out.URL.Path == "" {
				r.Out.URL.Path = "/"
			}
			// Kratos builds redirect and form URLs from what it believes the
			// browser asked for. Without these it would send people to the
			// in-cluster service name, which resolves nowhere useful from a
			// laptop.
			r.Out.Host = r.In.Host
			r.SetXForwarded()
		},
	}
	return p
}

// handleKratosFlow draws login, registration and settings. Kratos redirects the
// browser here with ?flow=<id> after it has created one.
func (s *Server) handleKratosFlow(kind, title, heading, lead, foot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("flow")
		if id == "" {
			// Arrived without a flow — someone typed the URL, or a link went
			// stale. Ask Kratos to start one; it comes back here with an id.
			http.Redirect(w, r, "/kratos/self-service/"+kind+"/browser", http.StatusFound)
			return
		}
		flow, err := s.kratos.Flow(r, kind, id)
		if err != nil {
			// An expired or unknown flow is the common case here, and the fix
			// is always a fresh one rather than an error page about it.
			http.Redirect(w, r, "/kratos/self-service/"+kind+"/browser", http.StatusFound)
			return
		}
		v := flowView(flow, title, heading, lead, foot)
		writeFlowPage(w, http.StatusOK)
		_ = ui.Flow(v).Render(r.Context(), w)
	}
}

// handleKratosError draws whatever Kratos refused to do.
func (s *Server) handleKratosError(w http.ResponseWriter, r *http.Request) {
	v := ui.FlowView{
		Title:   "Error",
		Heading: "That did not work",
		Lead:    "Something went wrong signing you in.",
		Foot:    "Try again from the start.",
		Buttons: []ui.FlowButton{},
	}
	if id := r.URL.Query().Get("id"); id != "" {
		if flow, err := s.kratos.Flow(r, "error", id); err == nil {
			for _, m := range flow.UI.Messages {
				v.Messages = append(v.Messages, ui.FlowMessage{Text: m.Text, Kind: "bad"})
			}
		}
	}
	// Always a way out: a dead end with no button is how someone ends up
	// clearing cookies to sign in.
	v.Action, v.Method = "/kratos/self-service/login/browser", "get"
	v.Buttons = []ui.FlowButton{{Label: "Start again"}}
	writeFlowPage(w, http.StatusOK)
	_ = ui.Flow(v).Render(r.Context(), w)
}

// flowView reduces a Kratos flow to what the page draws.
func flowView(f *auth.UIFlow, title, heading, lead, foot string) ui.FlowView {
	v := ui.FlowView{
		Title:   title,
		Heading: heading,
		Lead:    lead,
		Foot:    foot,
		Action:  f.UI.Action,
		Method:  strings.ToLower(f.UI.Method),
	}
	if v.Method == "" {
		v.Method = "post"
	}
	for _, m := range f.UI.Messages {
		kind := "warn"
		if m.Type == "error" {
			kind = "bad"
		}
		v.Messages = append(v.Messages, ui.FlowMessage{Text: m.Text, Kind: kind})
	}
	for _, n := range f.UI.Nodes {
		if n.Attributes.Disabled {
			continue
		}
		val, _ := n.Attributes.Value.(string)
		switch n.Attributes.Type {
		case "hidden":
			v.Hidden = append(v.Hidden, ui.FlowField{Name: n.Attributes.Name, Value: val})
		case "submit":
			label := n.Meta.Label.Text
			if label == "" {
				label = "Continue"
			}
			// Kratos labels a provider button with the provider's id, which is
			// an implementation detail of the config ("Sign in with access").
			// The person reading it signed in with Google.
			if n.Group == "oidc" {
				label = "Continue with Google"
			}
			v.Buttons = append(v.Buttons, ui.FlowButton{
				Name: n.Attributes.Name, Value: val, Label: label,
			})
		}
	}
	return v
}

func writeFlowPage(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'")
	w.WriteHeader(status)
}
