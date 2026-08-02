package controller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
	"github.com/dougkirkley/dross/internal/config"
)

// podTemplateFor builds the pod template that runs one session.
//
// The pod executes model-chosen code, so it is the least trusted thing in the
// cluster and is specced accordingly: unprivileged, no capabilities, read-only
// root, and no service-account token — it has no business talking to the API
// server. It dials out to the relay instead of listening, so nothing needs to
// reach it either.
func podTemplateFor(s *dross.Session) corev1.PodTemplateSpec {
	run := int64(10001)
	yes := true
	no := false

	args := []string{
		"rc",
		"--server", s.Spec.ServerURL,
		"--name", s.Spec.SessionID, // the relay device id the token authorises
		"--workdir", "/workspace",
	}
	if s.Spec.Model != "" {
		args = append(args, "--model", s.Spec.Model)
	}
	// Provider and base URL as arguments: they are not secret, and having them
	// visible on the pod is useful when working out what a session talked to.
	if s.Spec.Provider != "" {
		args = append(args, "--provider", s.Spec.Provider)
	}
	if s.Spec.BaseURL != "" {
		args = append(args, "--base-url", s.Spec.BaseURL)
	}

	// From a Secret rather than arguments, so credentials do not show up in the
	// pod spec or in `kubectl get pod -o yaml`.
	fromSecret := func(name, key string) corev1.EnvVar {
		return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: s.Spec.TokenSecret},
				Key:                  key,
				// Optional: a session created before the key was carried, or a
				// provider that takes no key, still starts.
				Optional: &yes,
			},
		}}
	}
	env := []corev1.EnvVar{fromSecret("DROSS_TOKEN", "token")}
	// The provider's own variable, which the CLI already reads when no key is
	// given on the command line.
	if v := config.APIKeyEnv(s.Spec.Provider); v != "" {
		env = append(env, fromSecret(v, "api-key"))
	}

	c := corev1.Container{
		Name:  "session",
		Image: s.Spec.Image,
		Args:  args,
		Env:   env,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &no,
			ReadOnlyRootFilesystem:   &yes,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    parseOr(s.Spec.CPULimit, "2"),
				corev1.ResourceMemory: parseOr(s.Spec.MemoryLimit, "2Gi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "tmp", MountPath: "/tmp"},
			{Name: "home", MountPath: "/home/dross"},
		},
	}

	// "workspace" is intentionally absent: it is supplied by the StatefulSet's
	// volumeClaimTemplate, which is what gives a session the same volume back
	// after a restart or a node reboot.
	volumes := []corev1.Volume{
		{Name: "tmp", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrQty("1Gi")},
		}},
		{Name: "home", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrQty("256Mi")},
		}},
	}
	if s.Spec.ConfigMap != "" {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name: "config", MountPath: "/etc/dross", ReadOnly: true,
		})
		c.Env = append(c.Env, corev1.EnvVar{Name: "DROSS_CONFIG", Value: "/etc/dross/config.json"})
		volumes = append(volumes, corev1.Volume{Name: "config", VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: s.Spec.ConfigMap},
			},
		}})
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labelsFor(s)},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: &no,
			// Always: a session survives a crash or a node reboot; the
			// StatefulSet brings it back with the same name and volume.
			RestartPolicy:      corev1.RestartPolicyAlways,
			EnableServiceLinks: &no,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   &yes,
				RunAsUser:      &run,
				RunAsGroup:     &run,
				FSGroup:        &run,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{c},
			Volumes:    volumes,
		},
	}
}

func parseOr(v, def string) resource.Quantity {
	if v == "" {
		return resource.MustParse(def)
	}
	q, err := resource.ParseQuantity(v)
	if err != nil {
		return resource.MustParse(def)
	}
	return q
}

func ptrQty(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
