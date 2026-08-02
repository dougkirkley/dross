// Package v1alpha1 defines the Session custom resource: one agent session,
// reconciled into a pod and a volume of its own.
//
// Modelling a session as a resource rather than imperatively creating pods buys
// three things. Deleting the Session garbage-collects its pod and volume through
// ownerReferences, so a crash between the two deletes cannot leak a volume.
// Reconciliation heals drift — a pod evicted by the node comes back without the
// server noticing. And status is a first-class field the API can read, instead
// of the server polling and caching pod phase itself.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SessionIDLabel carries the dross session id onto the objects created for it.
//
// The workload's name is generated and does not encode the id, so this is what
// correlates a pod back to the session it is running — and what the server uses
// to find a session again in order to delete it.
//
// It is deliberately not part of a StatefulSet's selector, which is immutable:
// it goes on the pod template and on object metadata, where it can be selected
// without pinning the shape of anything already running.
const SessionIDLabel = "dross.sh/session-id"

// BusyLabel marks a session whose agent is part-way through a turn.
//
// Set by the server, which is the only side that knows: the pod is equally
// healthy either way, so nothing about the workload distinguishes a session
// thinking from one sitting idle. The operator reads it to leave a session
// alone while it is working, and take it on a later pass.
const BusyLabel = "dross.sh/busy"

// GroupVersion is the API group/version this package registers.
var GroupVersion = schema.GroupVersion{Group: "dross.sh", Version: "v1alpha1"}

// SessionSpec is the desired state of one agent session.
type SessionSpec struct {
	// Owner is the authenticated identity the session belongs to. The server
	// enforces access on it; it is recorded here so a session's pod can be
	// attributed without consulting the server's own store.
	Owner string `json:"owner"`

	// SessionID is the dross session id. It is also the relay device id the pod
	// attaches as, and the scoped credential is bound to it.
	SessionID string `json:"sessionID"`

	// Image is the dross image the session runs.
	Image string `json:"image"`

	// ServerURL is the in-cluster address the pod dials back to.
	ServerURL string `json:"serverURL"`

	// TokenSecret names a Secret holding the scoped device token, so the
	// credential never appears in the pod spec or in `kubectl get pod -o yaml`.
	TokenSecret string `json:"tokenSecret"`

	// Provider and BaseURL are the model backend this session talks to, carried
	// from the server so a pod runs against the same one rather than falling
	// back to the built-in default. The API key is not here: it travels in
	// TokenSecret, because a spec is readable by anyone who can read the
	// resource.
	Provider string `json:"provider,omitempty"`
	BaseURL  string `json:"baseURL,omitempty"`

	// APIKeyEnv is the variable the key in TokenSecret is exposed as, decided by
	// the server because it is the side that knows the provider conventions.
	// Passing the name rather than the provider keeps the operator free of the
	// agent's configuration packages, and its image is deliberately small.
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`

	// Model overrides the server default for this session.
	Model string `json:"model,omitempty"`

	// Storage is the size of the session's workspace volume (e.g. "5Gi").
	Storage string `json:"storage,omitempty"`

	// StorageClass for the workspace volume; empty uses the cluster default.
	StorageClass string `json:"storageClass,omitempty"`

	// CPULimit and MemoryLimit bound the session, so one cannot starve another.
	CPULimit    string `json:"cpuLimit,omitempty"`
	MemoryLimit string `json:"memoryLimit,omitempty"`

	// ConfigMap is an optional dross config mounted read-only.
	ConfigMap string `json:"configMap,omitempty"`
}

// SessionStatus is the observed state.
type SessionStatus struct {
	// Phase mirrors the pod: "", Pending, Running, Succeeded, Failed.
	Phase string `json:"phase,omitempty"`

	// PodName is the pod backing this session.
	PodName string `json:"podName,omitempty"`

	// Message explains a phase that needs explaining (image pull failure, an
	// unschedulable pod), so the cause is visible without reading events.
	Message string `json:"message,omitempty"`

	// Conditions follow the standard convention, so kubectl and any tooling
	// that understands conditions can report on a session.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Session is one agent session.
type Session struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SessionSpec   `json:"spec,omitempty"`
	Status SessionStatus `json:"status,omitempty"`
}

// SessionList is a list of Sessions.
type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Session `json:"items"`
}

// --- runtime.Object plumbing -----------------------------------------------
//
// Written by hand rather than generated: it is a few dozen lines for two types,
// and it keeps controller-gen out of the build.

// DeepCopyInto copies the receiver into out.
func (in *Session) DeepCopyInto(out *Session) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy returns a deep copy.
func (in *Session) DeepCopy() *Session {
	if in == nil {
		return nil
	}
	out := new(Session)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject satisfies runtime.Object.
func (in *Session) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the status, including its conditions slice.
func (in *SessionStatus) DeepCopyInto(out *SessionStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// DeepCopyInto copies the list.
func (in *SessionList) DeepCopyInto(out *SessionList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Session, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy of the list.
func (in *SessionList) DeepCopy() *SessionList {
	if in == nil {
		return nil
	}
	out := new(SessionList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject satisfies runtime.Object.
func (in *SessionList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// AddToScheme registers the Session types with s.
func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &Session{}, &SessionList{}, &Dross{}, &DrossList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
