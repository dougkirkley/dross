package podrunner

import (
	"encoding/json"
	"strings"
	"testing"
)

// Session ids contain underscores, which Kubernetes object names forbid.
func TestObjectNameIsValid(t *testing.T) {
	got := name("ses_3c8791e54b33a7b7")
	if strings.Contains(got, "_") {
		t.Errorf("name %q contains an underscore", got)
	}
	if got != "dross-3c8791e54b33a7b7" {
		t.Errorf("name = %q", got)
	}
}

// The session pod runs model-chosen code, so its spec must be at least as
// locked down as the server's — and must not carry an API token.
func TestPodSpecIsHardened(t *testing.T) {
	spec := Spec{SessionID: "ses_abc", Image: "img", ServerURL: "http://svc", DeviceToken: "dv1.x"}
	b, err := json.Marshal(podFor(spec, "media"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)

	for _, want := range []string{
		`"automountServiceAccountToken":false`, // no cluster credentials in the pod
		`"runAsNonRoot":true`,
		`"readOnlyRootFilesystem":true`,
		`"allowPrivilegeEscalation":false`,
		`"drop":["ALL"]`,
		`"restartPolicy":"Never"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pod spec missing %s", want)
		}
	}
	// The device token is passed as an argument, and is scoped to this session.
	if !strings.Contains(body, "dv1.x") || !strings.Contains(body, "ses_abc") {
		t.Error("pod is not wired to its own scoped relay credential")
	}
}
