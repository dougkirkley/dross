package podrunner

import (
	"context"
	"fmt"
	"strings"
)

// Spec describes the session pod to create.
type Spec struct {
	SessionID    string // dross session id; also the device id it attaches as
	Owner        string // identity the session belongs to (label only)
	Image        string // dross image to run
	ServerURL    string // in-cluster URL the pod dials back to
	DeviceToken  string // scoped credential; opens the relay for SessionID only
	Model        string
	StorageClass string
	StorageSize  string // e.g. "5Gi"
	CPULimit     string
	MemoryLimit  string
	ConfigMap    string // optional: dross config mounted read-only
	ImagePullSec string // optional: pull secret name
}

// name is the Kubernetes object name for a session's pod and volume. Session
// ids are already `ses_<hex>`; underscores are not valid in object names.
func name(sessionID string) string {
	return "dross-" + strings.ReplaceAll(strings.TrimPrefix(sessionID, "ses_"), "_", "-")
}

// Create provisions the volume and pod for a session. Both are labelled so a
// stale set can be found and reaped, and the pod is owned by nothing — the
// server deletes it explicitly when the session goes away.
func (c *Client) Create(ctx context.Context, s Spec) error {
	if err := c.createPVC(ctx, s); err != nil && !IsAlreadyExists(err) {
		return fmt.Errorf("session volume: %w", err)
	}
	if err := c.createPod(ctx, s); err != nil && !IsAlreadyExists(err) {
		return fmt.Errorf("session pod: %w", err)
	}
	return nil
}

// Delete removes a session's pod and volume. Missing objects are not an error:
// the caller's intent is that they should not exist.
func (c *Client) Delete(ctx context.Context, sessionID string) error {
	n := name(sessionID)
	perr := c.do(ctx, "DELETE", fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", c.namespace, n), nil, nil)
	if perr != nil && !IsNotFound(perr) {
		return perr
	}
	verr := c.do(ctx, "DELETE",
		fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims/%s", c.namespace, n), nil, nil)
	if verr != nil && !IsNotFound(verr) {
		return verr
	}
	return nil
}

// Phase reports the pod's current phase ("Pending", "Running", ...), or "" when
// the pod does not exist.
func (c *Client) Phase(ctx context.Context, sessionID string) (string, error) {
	var out struct {
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}
	err := c.do(ctx, "GET",
		fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", c.namespace, name(sessionID)), nil, &out)
	if IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return out.Status.Phase, nil
}

func (c *Client) createPVC(ctx context.Context, s Spec) error {
	size := s.StorageSize
	if size == "" {
		size = "5Gi"
	}
	pvc := map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]any{
			"name":   name(s.SessionID),
			"labels": labels(s),
		},
		"spec": map[string]any{
			"accessModes": []string{"ReadWriteOnce"},
			"resources":   map[string]any{"requests": map[string]string{"storage": size}},
		},
	}
	if s.StorageClass != "" {
		pvc["spec"].(map[string]any)["storageClassName"] = s.StorageClass
	}
	return c.do(ctx, "POST",
		fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims", c.namespace), pvc, nil)
}

// podFor builds the Pod object. Split out from createPod so the security
// posture can be asserted in a test without an API server.
func podFor(s Spec, namespace string) map[string]any {
	n := name(s.SessionID)
	args := []string{
		"rc",
		"--server", s.ServerURL,
		"--token", s.DeviceToken,
		"--name", s.SessionID, // the relay device id the token authorises
		"--workdir", "/workspace",
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}

	container := map[string]any{
		"name":  "session",
		"image": s.Image,
		"args":  args,
		// The agent runs model-chosen code. Same posture as the server pod:
		// unprivileged, no capabilities, nothing writable but the mounts.
		"securityContext": map[string]any{
			"allowPrivilegeEscalation": false,
			"readOnlyRootFilesystem":   true,
			"capabilities":             map[string]any{"drop": []string{"ALL"}},
		},
		"resources": map[string]any{
			"requests": map[string]string{"cpu": "50m", "memory": "128Mi"},
			"limits": map[string]string{
				"cpu":    orDefault(s.CPULimit, "2"),
				"memory": orDefault(s.MemoryLimit, "2Gi"),
			},
		},
		"volumeMounts": []map[string]any{
			{"name": "workspace", "mountPath": "/workspace"},
			{"name": "tmp", "mountPath": "/tmp"},
			{"name": "home", "mountPath": "/home/dross"},
		},
	}
	volumes := []map[string]any{
		{"name": "workspace", "persistentVolumeClaim": map[string]string{"claimName": n}},
		{"name": "tmp", "emptyDir": map[string]any{"sizeLimit": "1Gi"}},
		{"name": "home", "emptyDir": map[string]any{"sizeLimit": "256Mi"}},
	}
	if s.ConfigMap != "" {
		container["volumeMounts"] = append(container["volumeMounts"].([]map[string]any),
			map[string]any{"name": "config", "mountPath": "/etc/dross", "readOnly": true})
		container["env"] = []map[string]string{{"name": "DROSS_CONFIG", "value": "/etc/dross/config.json"}}
		volumes = append(volumes, map[string]any{
			"name": "config", "configMap": map[string]string{"name": s.ConfigMap}})
	}

	spec := map[string]any{
		// The session dials out; nothing dials in, and it never needs the API.
		"automountServiceAccountToken": false,
		"restartPolicy":                "Never",
		"enableServiceLinks":           false,
		"securityContext": map[string]any{
			"runAsNonRoot":   true,
			"runAsUser":      10001,
			"runAsGroup":     10001,
			"fsGroup":        10001,
			"seccompProfile": map[string]string{"type": "RuntimeDefault"},
		},
		"containers": []map[string]any{container},
		"volumes":    volumes,
	}
	if s.ImagePullSec != "" {
		spec["imagePullSecrets"] = []map[string]string{{"name": s.ImagePullSec}}
	}

	pod := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":   n,
			"labels": labels(s),
		},
		"spec": spec,
	}
	return pod
}

func (c *Client) createPod(ctx context.Context, s Spec) error {
	return c.do(ctx, "POST", fmt.Sprintf("/api/v1/namespaces/%s/pods", c.namespace),
		podFor(s, c.namespace), nil)
}

// labels identify a session's objects. The owner is recorded as a label value,
// which must be a valid label — emails are not, so it is only the local part
// plus a hash-free marker; ownership is enforced server-side, not by labels.
func labels(s Spec) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      "dross-session",
		"app.kubernetes.io/component": "agent",
		"dross.sh/session":            name(s.SessionID),
	}
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
