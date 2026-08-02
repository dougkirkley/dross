package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Dross is a dross server: the API, the embedded web client, and the settings
// its sessions are created with.
//
// The operator owns the server as well as the sessions, so that the two cannot
// disagree. The server has to be told which namespace to put sessions in, which
// image they run, and where they dial back to; every one of those is something
// the operator already knows, and leaving them to be written by hand in a
// Deployment is how they drift apart.
type Dross struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DrossSpec   `json:"spec,omitempty"`
	Status DrossStatus `json:"status,omitempty"`
}

// DrossSpec is the desired state of a dross server.
type DrossSpec struct {
	// Image is the dross image. One binary serves the API and runs a session,
	// so the server and the session pods it asks for are the same image, and
	// there is no version skew to reason about between them.
	Image string `json:"image"`

	// Replicas of the server. Its volumes are ReadWriteOnce, so more than one
	// cannot attach them; this exists to allow scaling to zero.
	Replicas *int32 `json:"replicas,omitempty"`

	// Args passed to the server, including the subcommand. $(VAR) references
	// are expanded from AuthSecret.
	Args []string `json:"args,omitempty"`

	// AuthSecret holds the SSO credentials the args refer to: client-id,
	// client-secret, cookie-secret and service-token.
	AuthSecret string `json:"authSecret,omitempty"`

	// ConfigMap is mounted read-only at /etc/dross.
	ConfigMap string `json:"configMap,omitempty"`

	// Claims are the volumes the server keeps its state on.
	Claims DrossClaims `json:"claims,omitempty"`

	// Resources for the server container.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// ServiceAccountName the server runs as. It needs one to write Sessions.
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Sessions are the defaults every session this server creates is given.
	Sessions SessionDefaults `json:"sessions,omitempty"`
}

// DrossClaims names the PersistentVolumeClaims the server mounts. They are not
// created here: they outlive any one server and are managed alongside the rest
// of the cluster's storage.
type DrossClaims struct {
	// Workspace is the sandbox root sessions run under.
	Workspace string `json:"workspace,omitempty"`
	// Artifacts holds published files, which must outlive the session that
	// produced them so a shared link still resolves.
	Artifacts string `json:"artifacts,omitempty"`
	// Data holds session history and the user registry.
	Data string `json:"data,omitempty"`
}

// SessionDefaults configures the sessions a server creates. The operator turns
// these into the server's environment, so the server does not have to be
// configured separately from the controller that serves its requests.
type SessionDefaults struct {
	// Namespace session workloads are created in. Empty turns pod-per-session
	// off, and the server runs sessions in its own process instead.
	Namespace string `json:"namespace,omitempty"`

	// Storage is the size of each session's workspace volume.
	Storage string `json:"storage,omitempty"`

	// StorageClass for session volumes; empty uses the cluster default.
	StorageClass string `json:"storageClass,omitempty"`

	// CPULimit and MemoryLimit bound a session, so one cannot starve another.
	CPULimit    string `json:"cpuLimit,omitempty"`
	MemoryLimit string `json:"memoryLimit,omitempty"`

	// ConfigMap is an optional dross config mounted into each session.
	ConfigMap string `json:"configMap,omitempty"`
}

// DrossStatus is the observed state.
type DrossStatus struct {
	// Phase is Pending while the server is coming up, Running once it is, and
	// Paused at zero replicas.
	Phase string `json:"phase,omitempty"`

	// ReadyReplicas of the server deployment.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// SessionMode reads "pods" or "in-process", so which mode a server ended up
	// in is visible without reading its environment.
	SessionMode string `json:"sessionMode,omitempty"`

	// Message explains a phase that needs explaining.
	Message string `json:"message,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DrossList is a list of Dross servers.
type DrossList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Dross `json:"items"`
}

// --- runtime.Object plumbing -----------------------------------------------

// DeepCopyInto copies the receiver into out.
func (in *Dross) DeepCopyInto(out *Dross) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy.
func (in *Dross) DeepCopy() *Dross {
	if in == nil {
		return nil
	}
	out := new(Dross)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject satisfies runtime.Object.
func (in *Dross) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the spec, including its slices and nested resources.
func (in *DrossSpec) DeepCopyInto(out *DrossSpec) {
	*out = *in
	out.Claims = in.Claims
	out.Sessions = in.Sessions
	if in.Replicas != nil {
		r := *in.Replicas
		out.Replicas = &r
	}
	if in.Args != nil {
		out.Args = make([]string, len(in.Args))
		copy(out.Args, in.Args)
	}
	in.Resources.DeepCopyInto(&out.Resources)
}

// DeepCopyInto copies the status, including its conditions slice.
func (in *DrossStatus) DeepCopyInto(out *DrossStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopyInto copies the list.
func (in *DrossList) DeepCopyInto(out *DrossList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Dross, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy of the list.
func (in *DrossList) DeepCopy() *DrossList {
	if in == nil {
		return nil
	}
	out := new(DrossList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject satisfies runtime.Object.
func (in *DrossList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
