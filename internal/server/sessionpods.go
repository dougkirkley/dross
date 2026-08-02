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
func (p *PodSessions) Create(ctx context.Context, sessionID, owner, model string) (string, error) {
	friendly := friendlyName()
	name := sessionName(p.NamePrefix, friendly)
	labels := map[string]string{sessionIDLabel: sessionID}

	tok, err := p.MintToken(owner, sessionID, p.TokenTTL)
	if err != nil {
		return "", fmt.Errorf("mint session credential: %w", err)
	}
	data := map[string]string{"token": tok}
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
	if err := p.c.Create(ctx, s); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("session resource: %w", err)
	}
	return friendly, nil
}

// legacyObjectName is how a workload was named before the label existed:
// "dross-" and the session id, with the characters an object name forbids
// removed.
func legacyObjectName(sessionID string) string {
	return "dross-" + strings.ReplaceAll(strings.TrimPrefix(sessionID, "ses_"), "_", "-")
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
