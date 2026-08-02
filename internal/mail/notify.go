package mail

import (
	"bytes"
	"context"
	"html/template"
	"strings"
)

// Account is the little dross tells the mailer about a person. Deliberately not
// the server's User type: the composed copy must not have to change because a
// field was added to the registry.
type Account struct {
	Email string
	Name  string
}

// Greeting is the name to address someone by, falling back to the local part of
// their address so no mail opens with "Hi ,".
func (a Account) Greeting() string {
	if n := strings.TrimSpace(a.Name); n != "" {
		return n
	}
	local, _, _ := strings.Cut(a.Email, "@")
	if local == "" {
		return "there"
	}
	return local
}

// Notifier is the account mail dross owes people. The application depends on
// this, not on how the copy is written or who carries it, so an implementation
// backed by a template service, a queue, or nothing at all drops in unchanged.
type Notifier interface {
	// RequestReceived acknowledges a new account to the person who made it.
	RequestReceived(ctx context.Context, a Account) error
	// AdminRequest tells one administrator that someone is waiting. The caller
	// decides who they are: that is a question about the directory, not about
	// mail, and there can be more than one answer.
	AdminRequest(ctx context.Context, to string, a Account) error
	// Welcome is the mail an approval sends.
	Welcome(ctx context.Context, a Account) error
	// Enabled reports whether anything is actually delivered, so a UI can say
	// "no mail configured" instead of implying a mail went out.
	Enabled() bool
}

// Templated is the built-in Notifier: dross's own copy, handed to a Sender.
// Every method is a no-op when Send is nil, so approval works on a server with
// no mail configured.
type Templated struct {
	Send   Sender
	AppURL string // public origin, e.g. https://dross.sh; used for links
}

var _ Notifier = (*Templated)(nil)

// Enabled reports whether a transport is configured. Nil-safe, and checked at
// the top of every method: a nil *Templated held in a Notifier is a working
// "send nothing", so it must not be dereferenced on the way to finding that out.
func (n *Templated) Enabled() bool { return n != nil && n.Send != nil }

// RequestReceived tells someone their sign-in registered a request.
func (n *Templated) RequestReceived(ctx context.Context, a Account) error {
	if !n.Enabled() {
		return nil
	}
	return n.deliver(ctx, a.Email, "Your Dross access request", requestTmpl,
		tmplData{Account: a, AppURL: n.AppURL})
}

// AdminRequest tells an administrator someone is waiting, with a link to the
// page that decides it.
func (n *Templated) AdminRequest(ctx context.Context, to string, a Account) error {
	if !n.Enabled() || strings.TrimSpace(to) == "" {
		return nil
	}
	return n.deliver(ctx, to, "Access request: "+a.Email, adminTmpl,
		tmplData{Account: a, AppURL: n.AppURL, AdminURL: strings.TrimRight(n.AppURL, "/") + "/admin"})
}

// Welcome is the mail an approval sends.
func (n *Templated) Welcome(ctx context.Context, a Account) error {
	if !n.Enabled() {
		return nil
	}
	return n.deliver(ctx, a.Email, "You're in — welcome to Dross", welcomeTmpl,
		tmplData{Account: a, AppURL: n.AppURL})
}

type tmplData struct {
	Account
	AppURL   string
	AdminURL string
}

type mailTmpl struct {
	html *template.Template
	text *template.Template
}

func (n *Templated) deliver(ctx context.Context, to, subject string, t mailTmpl, d tmplData) error {
	if !n.Enabled() || strings.TrimSpace(to) == "" {
		return nil
	}
	m := Message{To: to, Subject: subject}
	var h, x bytes.Buffer
	if err := t.html.Execute(&h, d); err != nil {
		return err
	}
	if err := t.text.Execute(&x, d); err != nil {
		return err
	}
	m.HTML, m.Text = h.String(), x.String()
	return n.Send.Send(ctx, m)
}

// --- copy ------------------------------------------------------------------

// shell wraps body copy in the same card the app's own pages use: dark ground,
// #0E0E1A card on a #241E3B hairline, the mascot at the top. Built as a table
// with inline styles because a mail client is not a browser — Gmail strips
// <style>, Outlook ignores flexbox, and neither renders the SVG the web pages
// use, so the mark comes in as the PNG the app already serves at /icons.
//
// The palette is the app's, not a mail-safe approximation of it: an approval
// mail that looks like the thing it grants access to is doing its job.
func shell(body string) string {
	return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="color-scheme" content="dark">
<meta name="supported-color-schemes" content="dark">
</head>
<body style="margin:0;padding:24px;background:#07070F;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif;color:#ECE9FB;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#07070F;"><tr><td align="center">
<table role="presentation" width="480" cellpadding="0" cellspacing="0" style="max-width:480px;background:#0E0E1A;border:1px solid #241E3B;border-radius:18px;">
<tr><td align="center" style="padding:36px 32px 4px;">
  <img src="{{.AppURL}}/icons/Icon-192.png" width="72" height="72" alt="" style="display:block;width:72px;height:72px;border:0;">
</td></tr>
<tr><td align="center" style="padding:14px 32px 0;">
  <div style="font-size:24px;font-weight:650;letter-spacing:-0.02em;color:#ECE9FB;">Dross</div>
</td></tr>
<tr><td style="padding:10px 32px 32px;font-size:15px;line-height:1.6;color:#B4B0D8;">
` + body + `
</td></tr>
<tr><td style="padding:0 32px;"><div style="height:1px;background:#8B5CF6;opacity:0.55;font-size:0;line-height:0;">&nbsp;</div></td></tr>
<tr><td align="center" style="padding:14px 32px 24px;font-size:12.5px;color:#55547A;">your own coding agent</td></tr>
</table>
</td></tr></table>
</body></html>`
}

// btn is the app's primary button: a light pill on the dark card.
func btn(label string) string {
	return `<p style="margin:26px 0 4px;"><a href="{{.AppURL}}" style="display:inline-block;padding:13px 22px;background:#F4F1FC;color:#12101F;border-radius:12px;text-decoration:none;font-weight:600;font-size:15px;">` + label + `</a></p>`
}

var requestTmpl = mailTmpl{
	html: template.Must(template.New("request.html").Parse(shell(
		`<p>Hi {{.Greeting}},</p>
<p>Your request to use Dross is in. Someone has to approve the account before it
can do anything, and you'll get another mail the moment that happens.</p>
<p style="color:#55547A;">Nothing to do until then.</p>`))),
	text: template.Must(template.New("request.txt").Parse(
		`Hi {{.Greeting}},

Your request to use Dross is in. Someone has to approve the account before it
can do anything, and you'll get another mail the moment that happens.

Nothing to do until then.
`)),
}

var adminTmpl = mailTmpl{
	html: template.Must(template.New("admin.html").Parse(shell(
		`<p><strong>{{.Email}}</strong>{{if .Name}} ({{.Name}}){{end}} signed in and is waiting to be approved.</p>
<p style="margin:24px 0 8px;"><a href="{{.AdminURL}}" style="display:inline-block;padding:12px 20px;background:#6320C9;color:#FFFFFF;border-radius:10px;text-decoration:none;font-weight:600;">Review the request</a></p>
<p style="color:#55547A;font-size:13px;">Until you approve them they can reach the sign-in and nothing else.</p>`))),
	text: template.Must(template.New("admin.txt").Parse(
		`{{.Email}}{{if .Name}} ({{.Name}}){{end}} signed in and is waiting to be approved.

Review the request: {{.AdminURL}}

Until you approve them they can reach the sign-in and nothing else.
`)),
}

var welcomeTmpl = mailTmpl{
	html: template.Must(template.New("welcome.html").Parse(shell(
		`<p>Hi {{.Greeting}},</p>
<p>Your Dross account is approved. Sign in and it's yours — sessions you start
keep running whether or not you're watching, and you can pick one back up from
the web app, the phone, or the terminal.</p>` + btn("Open Dross")))),
	text: template.Must(template.New("welcome.txt").Parse(
		`Hi {{.Greeting}},

Your Dross account is approved. Sign in and it's yours - sessions you start keep
running whether or not you're watching, and you can pick one back up from the
web app, the phone, or the terminal.

{{.AppURL}}
`)),
}
