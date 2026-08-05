package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
	"github.com/dougkirkley/dross/internal/config"
)

// Pod-backed sessions.
//
// The server does not create pods — it has no permission to, deliberately. It
// writes a Session record and the operator turns that into a workload. That
// split means a compromise of this process, which is internet-facing and
// handles model output, cannot schedule anything; it can only ask.

// PodSessions provisions per-session workloads by writing Session resources.
type PodSessions struct {
	c         client.Client
	namespace string

	// Image the session pods run, and where they dial back to.
	Image     string
	ServerURL string

	// NamePrefix starts each workload's name, so a session reads as belonging
	// to the server that asked for it.
	NamePrefix string

	// Provider, BaseURL and APIKey are the model backend the server itself uses.
	// A session pod is handed the same one, or it would fall back to the CLI's
	// built-in default and fail on the first prompt with no credentials. The key
	// goes into the session's Secret; the other two into its spec.
	Provider string
	BaseURL  string
	APIKey   string

	// Defaults applied to each session's workload.
	Storage      string
	StorageClass string
	CPULimit     string
	MemoryLimit  string
	ConfigMap    string

	// MintToken issues the scoped relay credential for a session.
	MintToken func(owner, sessionID string, ttl time.Duration) (string, error)

	// TokenTTL bounds how long a session's credential stays valid.
	TokenTTL time.Duration
}

// NewPodSessions builds a client from the pod's own service account. Returns
// (nil, nil) outside a cluster, so a Docker or local deployment simply keeps
// running sessions in-process.
func NewPodSessions(namespace string) (*PodSessions, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		if err == rest.ErrNotInCluster {
			return nil, nil
		}
		return nil, err
	}
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := dross.AddToScheme(s); err != nil {
		return nil, err
	}
	c, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		return nil, err
	}
	return &PodSessions{c: c, namespace: namespace, TokenTTL: 24 * time.Hour}, nil
}

// sessionIDLabel ties a workload back to the dross session that asked for it.
// Shared with the operator, which puts the same label on the pod.
const sessionIDLabel = dross.SessionIDLabel

// Create writes the session's credential and its Session record. The operator
// takes it from there.
//
// Returns the friendly half of the workload's name, so the session can be
// called the same thing in the UI as the pod running it.
// clone is optional: nil starts the session on an empty workspace, which is
// every session that predates the git-host integration.
func (p *PodSessions) Create(ctx context.Context, sessionID, owner, model string, clone *CloneSpec) (string, error) {
	// A few hundred name pairs is enough to be readable and nowhere near enough
	// to assume uniqueness, so a taken name is drawn again. Swallowing the
	// conflict instead — which is what this did — left two sessions pointing at
	// one workload and showing the same name in the list.
	friendly, name, err := p.freeName(ctx)
	if err != nil {
		return "", err
	}
	labels := map[string]string{sessionIDLabel: sessionID}

	tok, mintErr := p.MintToken(owner, sessionID, p.TokenTTL)
	err = mintErr
	if err != nil {
		return "", fmt.Errorf("mint session credential: %w", err)
	}
	data := map[string]string{"token": tok}
	if clone != nil {
		// The credentialed URL is a credential, so it goes here and not in the
		// spec: a Session resource is readable by anything with get on the
		// namespace, and a URL with an access token in it is the user's private
		// source code to whoever reads it.
		data["clone-url"] = clone.Credentialed
	}
	if p.APIKey != "" {
		// Alongside the relay credential rather than in the spec: both are
		// secrets the pod resolves at start, and one Secret per session keeps
		// their lifetime tied to the session that owns them.
		data["api-key"] = p.APIKey
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: p.namespace, Name: name + "-token", Labels: labels,
		},
		StringData: data,
		Type:       corev1.SecretTypeOpaque,
	}
	if err := p.c.Create(ctx, sec); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("session credential: %w", err)
	}

	s := &dross.Session{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: p.namespace, Name: name, Labels: labels,
		},
		Spec: dross.SessionSpec{
			Owner:        owner,
			SessionID:    sessionID,
			Image:        p.Image,
			ServerURL:    p.ServerURL,
			TokenSecret:  sec.Name,
			Provider:     p.Provider,
			BaseURL:      p.BaseURL,
			APIKeyEnv:    config.APIKeyEnv(p.Provider),
			Model:        model,
			Storage:      p.Storage,
			StorageClass: p.StorageClass,
			CPULimit:     p.CPULimit,
			MemoryLimit:  p.MemoryLimit,
			ConfigMap:    p.ConfigMap,
		},
	}
	if clone != nil {
		// The plain half of the same thing. It is in the spec deliberately:
		// which repository a session is working on is what somebody looking at
		// the namespace most wants to know, and none of it is secret.
		s.Spec.Repo = clone.FullName
		s.Spec.RepoBranch = clone.Branch
		// Named for the session, which is already unique in this namespace and
		// is what the session is called everywhere else — so the branch in a
		// pull request says which session made it.
		s.Spec.WorkBranch = WorkBranch(friendly)
	}
	if err := p.c.Create(ctx, s); err != nil {
		return "", fmt.Errorf("session resource: %w", err)
	}
	return friendly, nil
}

// freeName draws a name that is not already taken in the session namespace.
//
// Checked rather than assumed: with a few hundred pairs, a handful of sessions
// is enough for a repeat, and two sessions sharing a name share the workload
// behind it.
func (p *PodSessions) freeName(ctx context.Context) (friendly, name string, err error) {
	const attempts = 8
	for i := 0; i < attempts; i++ {
		friendly = friendlyName()
		name = sessionName(p.NamePrefix, friendly)
		var existing dross.Session
		err = p.c.Get(ctx, client.ObjectKey{Namespace: p.namespace, Name: name}, &existing)
		if apierrors.IsNotFound(err) {
			return friendly, name, nil
		}
		if err != nil {
			return "", "", fmt.Errorf("checking session name: %w", err)
		}
	}
	return "", "", fmt.Errorf("no free session name after %d attempts", attempts)
}

// legacyObjectName is how a workload was named before the label existed:
// "dross-" and the session id, with the characters an object name forbids
// removed.
func legacyObjectName(sessionID string) string {
	return "dross-" + strings.ReplaceAll(strings.TrimPrefix(sessionID, "ses_"), "_", "-")
}

// SetBusy marks whether a session is part-way through a turn, so the operator
// can avoid replacing its pod mid-answer.
//
// Best effort and non-blocking for the caller: a session that fails to be
// marked is one that might be interrupted by a roll, which is the behaviour
// that existed before this and is not worth failing a prompt over.
func (p *PodSessions) SetBusy(ctx context.Context, sessionID string, busy bool) {
	found, err := p.findBySession(ctx, sessionID)
	if err != nil || found == nil {
		return
	}
	was := found.Labels[dross.BusyLabel] == "true"
	if was == busy {
		return
	}
	if found.Labels == nil {
		found.Labels = map[string]string{}
	}
	if busy {
		found.Labels[dross.BusyLabel] = "true"
	} else {
		delete(found.Labels, dross.BusyLabel)
	}
	_ = p.c.Update(ctx, found)
}

// findBySession returns the Session resource for a dross session id, since the
// object's name no longer encodes it.
func (p *PodSessions) findBySession(ctx context.Context, sessionID string) (*dross.Session, error) {
	var list dross.SessionList
	if err := p.c.List(ctx, &list,
		client.InNamespace(p.namespace),
		client.MatchingLabels{sessionIDLabel: sessionID},
	); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}

// Delete removes a session's record; ownerReferences reclaim the workload and
// its volume. The credential is deleted explicitly, since it is not owned.
func (p *PodSessions) Delete(ctx context.Context, sessionID string) error {
	found, err := p.findBySession(ctx, sessionID)
	if err != nil {
		return err
	}
	if found == nil {
		// Sessions created before workloads carried the label have none to find
		// them by, and their names encoded the session id instead. Without this
		// an upgrade silently leaks every workload and volume that existed
		// before it: the lookup returns nothing and delete reports success.
		found = &dross.Session{ObjectMeta: metav1.ObjectMeta{
			Namespace: p.namespace, Name: legacyObjectName(sessionID),
		}}
	}
	name := found.Name
	if err := p.c.Delete(ctx, found); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: p.namespace, Name: name + "-token"}}
	if err := p.c.Delete(ctx, sec); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
