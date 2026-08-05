package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
)

// serverLabels identify the server's pods. The Service selects on them, so
// they have to stay stable across a rename of the Dross object.
func serverLabels(d *dross.Dross) map[string]string {
	return map[string]string{"app.kubernetes.io/name": d.Name}
}

// serviceFor is the in-cluster address of the server. Session pods dial it, and
// so does the gateway.
func serviceFor(d *dross.Dross) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: d.Namespace, Name: d.Name, Labels: serverLabels(d),
		},
		Spec: corev1.ServiceSpec{
			Selector: serverLabels(d),
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 8080, TargetPort: intstr.FromString("http"),
			}},
		},
	}
}

// sessionEnv is what makes the server and the operator agree.
//
// The server needs to know where sessions go, what image they run and how to
// reach it; the operator knows all of that already. Deriving the environment
// here means the two cannot be configured differently — in particular the
// session image is the server's own image, because it is the same binary.
func sessionEnv(d *dross.Dross) []corev1.EnvVar {
	s := d.Spec.Sessions
	if s.Namespace == "" {
		// Pod mode off: the server runs sessions in its own process. Setting
		// nothing at all is what selects that, so say nothing.
		return nil
	}
	env := []corev1.EnvVar{
		{Name: "DROSS_SESSION_NAMESPACE", Value: s.Namespace},
		{Name: "DROSS_SESSION_IMAGE", Value: d.Spec.Image},
		// The Service this same reconcile creates, by name: the server does not
		// need to be told its own address by hand.
		{Name: "DROSS_SESSION_SERVER_URL", Value: "http://" + d.Name + "." + d.Namespace + ".svc:8080"},
		// Session workloads are named after this server, so which server asked
		// for a pod is legible from `kubectl get pods` alone.
		{Name: "DROSS_SESSION_PREFIX", Value: d.Name},
	}
	for _, kv := range []struct{ k, v string }{
		{"DROSS_SESSION_STORAGE", s.Storage},
		{"DROSS_SESSION_STORAGE_CLASS", s.StorageClass},
		{"DROSS_SESSION_CPU_LIMIT", s.CPULimit},
		{"DROSS_SESSION_MEMORY_LIMIT", s.MemoryLimit},
		{"DROSS_SESSION_CONFIGMAP", s.ConfigMap},
	} {
		if kv.v != "" {
			env = append(env, corev1.EnvVar{Name: kv.k, Value: kv.v})
		}
	}
	return env
}

// authEnv pulls the SSO credentials out of a Secret for the args to expand.
func authEnv(d *dross.Dross) []corev1.EnvVar {
	if d.Spec.AuthSecret == "" {
		return nil
	}
	ref := func(key string, optional bool) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: d.Spec.AuthSecret},
			Key:                  key,
			Optional:             &optional,
		}}
	}
	return []corev1.EnvVar{
		{Name: "AUTH_CLIENT_ID", ValueFrom: ref("client-id", false)},
		{Name: "AUTH_CLIENT_SECRET", ValueFrom: ref("client-secret", false)},
		{Name: "AUTH_COOKIE_SECRET", ValueFrom: ref("cookie-secret", false)},
		{Name: "AUTH_SERVICE_TOKEN", ValueFrom: ref("service-token", false)},
		// Account mail, read straight from the environment by the server rather
		// than passed as an arg. Optional: a deployment with no mail provider
		// must still start, and a missing key here would otherwise wedge the pod
		// in CreateContainerConfigError.
		{Name: "RESENDER_API_KEY", ValueFrom: ref("resend-key", true)},
		// Web search. Sets the built-in web_search to Exa instead of scraping a
		// results page, and is what the Exa MCP server authenticates with.
		// Optional for the same reason as the mail key: a deployment without one
		// falls back rather than failing to start.
		{Name: "EXA_API_KEY", ValueFrom: ref("exa-api-key", true)},
		// Git host OAuth applications, which is how a session gets a repository
		// to start on. The client id is ordinary config and lives in the
		// ConfigMap; only the secret comes from here, because a ConfigMap is
		// readable by anyone who can read the namespace and is kept in version
		// control besides.
		//
		// Optional, like the two above: a deployment that has registered no
		// OAuth application offers no repositories, and a missing key would
		// otherwise wedge the pod in CreateContainerConfigError rather than
		// leaving one feature switched off.
		// DROSS_-prefixed, matching every other variable this operator sets, so
		// that what dross put in a pod's environment is tellable from what the
		// image or the platform did. The config names which variable to read,
		// so the library's own default (GITHUB_CLIENT_SECRET, the conventional
		// name for anyone running dross outside Kubernetes) stays available to
		// deployments that would rather use it.
		{Name: "DROSS_GITHUB_SECRET", ValueFrom: ref("github-client-secret", true)},
		{Name: "DROSS_GITLAB_SECRET", ValueFrom: ref("gitlab-client-secret", true)},
	}
}

// deploymentFor builds the server workload.
// configHashAnnotation carries a digest of the mounted config, so that
// changing the config changes the pod template.
//
// Without it a config edit is invisible to the workload: Kubernetes updates
// the mounted file but never restarts the pod, so the server goes on running
// whatever it parsed at boot. That is not a theoretical gap — this deployment
// spent a day answering with a model the config no longer named, and served a
// model that had since been removed from the node entirely. Any pod-template
// field would do; an annotation is the one that means nothing to anything else.
const configHashAnnotation = "dross.sh/config-hash"

// deploymentFor builds the server workload. configHash is a digest of the
// mounted ConfigMap's contents, or "" when there is no config to mount.
func deploymentFor(d *dross.Dross, configHash string) *appsv1.Deployment {
	run := int64(10001)
	yes, no := true, false
	replicas := int32(1)
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}

	env := []corev1.EnvVar{
		// Sessions and the user registry live under the config dir, which Go
		// resolves from XDG_CONFIG_HOME. Point it at the data volume so they
		// survive a redeploy.
		{Name: "XDG_CONFIG_HOME", Value: "/data"},
	}
	if d.Spec.ConfigMap != "" {
		env = append(env, corev1.EnvVar{Name: "DROSS_CONFIG", Value: "/etc/dross/config.json"})
	}
	env = append(env, authEnv(d)...)
	env = append(env, sessionEnv(d)...)

	mounts := []corev1.VolumeMount{
		// The root filesystem is read-only, but /tmp still has to work:
		// multipart uploads spill there and plenty of tooling assumes it.
		{Name: "tmp", MountPath: "/tmp"},
	}
	volumes := []corev1.Volume{{
		Name: "tmp", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrQty("1Gi")},
		},
	}}
	if d.Spec.ConfigMap != "" {
		mounts = append(mounts, corev1.VolumeMount{Name: "config", MountPath: "/etc/dross", ReadOnly: true})
		volumes = append(volumes, corev1.Volume{Name: "config", VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: d.Spec.ConfigMap},
			},
		}})
	}
	for _, c := range []struct{ name, path, claim string }{
		{"workspace", "/workspace", d.Spec.Claims.Workspace},
		{"artifacts", "/artifacts", d.Spec.Claims.Artifacts},
		{"data", "/data", d.Spec.Claims.Data},
	} {
		if c.claim == "" {
			continue
		}
		mounts = append(mounts, corev1.VolumeMount{Name: c.name, MountPath: c.path})
		volumes = append(volumes, corev1.Volume{Name: c.name, VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: c.claim},
		}})
	}

	res := d.Spec.Resources
	if res.Requests == nil && res.Limits == nil {
		res = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("2"),
				// The pod hosts agent sessions and a Go toolchain, not just a
				// web server; 128Mi OOM-killed it under a large upload.
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		}
	}

	// /api/health stays public even when auth is enabled; probing the gated UI
	// would 401, since probes send no Accept: text/html.
	probe := func(delay, period int32) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
				Path: "/api/health", Port: intstr.FromString("http"),
			}},
			InitialDelaySeconds: delay, PeriodSeconds: period,
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: d.Namespace, Name: d.Name, Labels: serverLabels(d),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: serverLabels(d)},
			// Recreate, not a rolling update: the volumes are ReadWriteOnce, so
			// a surge pod scheduled onto another node can never attach them
			// while the outgoing pod holds them, and the rollout deadlocks on
			// Multi-Attach instead of converging.
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      serverLabels(d),
					Annotations: podAnnotations(configHash),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: d.Spec.ServiceAccountName,
					EnableServiceLinks: &no,
					// The agent runs model-chosen shell commands. Guards in the
					// app can be worked around with quoting, so the real
					// boundary is here: no root, no capabilities, and nothing
					// writable outside the mounted volumes.
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &yes,
						RunAsUser:    &run,
						RunAsGroup:   &run,
						// Volumes are chowned to this group so a non-root
						// process can write them.
						FSGroup:        &run,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:           d.Name,
						Image:          d.Spec.Image,
						Args:           d.Spec.Args,
						Ports:          []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
						Env:            env,
						ReadinessProbe: probe(3, 10),
						LivenessProbe:  probe(10, 30),
						Resources:      res,
						VolumeMounts:   mounts,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &no,
							ReadOnlyRootFilesystem:   &yes,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
					Volumes: volumes,
				},
			},
		},
	}
}

// podAnnotations stamps the config digest on the pod template when there is
// one. Nil rather than an empty map when there is not, so a deployment with no
// config keeps a pod template identical to the one it had before this existed
// and is not rolled by the upgrade that introduced it.
func podAnnotations(configHash string) map[string]string {
	if configHash == "" {
		return nil
	}
	return map[string]string{configHashAnnotation: configHash}
}

// hashConfigMap digests a ConfigMap's contents, stably.
//
// Keys are sorted because map iteration is not ordered, and each is written
// with its length so that no two different maps can serialise the same way.
// Binary data counts too: it is just as mounted, and just as capable of
// changing what the server does.
func hashConfigMap(cm *corev1.ConfigMap) string {
	h := sha256.New()
	keys := make([]string, 0, len(cm.Data)+len(cm.BinaryData))
	for k := range cm.Data {
		keys = append(keys, k)
	}
	for k := range cm.BinaryData {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "%d:%s=", len(k), k)
		if v, ok := cm.Data[k]; ok {
			fmt.Fprintf(h, "%d:%s;", len(v), v)
		}
		if v, ok := cm.BinaryData[k]; ok {
			fmt.Fprintf(h, "%d:%x;", len(v), v)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
