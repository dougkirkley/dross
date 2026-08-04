package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
)

func cm(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "c"}, Data: data}
}

// The bug this exists to prevent: a config edit that the workload never sees.
// Different contents must produce a different digest, or the pod template is
// unchanged and nothing rolls.
func TestHashConfigMapChangesWithContents(t *testing.T) {
	a := hashConfigMap(cm(map[string]string{"config.json": `{"model":"devstral"}`}))
	b := hashConfigMap(cm(map[string]string{"config.json": `{"model":"qwen2.5-coder:14b"}`}))
	if a == b {
		t.Fatal("a changed config must change the digest")
	}
	if again := hashConfigMap(cm(map[string]string{"config.json": `{"model":"devstral"}`})); again != a {
		t.Error("the same config must digest the same way, or every pass rolls the pods")
	}
}

// Map iteration is unordered, so a digest built by ranging naively would differ
// run to run and roll the deployment forever.
func TestHashConfigMapIsStableAcrossKeyOrder(t *testing.T) {
	many := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}
	first := hashConfigMap(cm(many))
	for i := 0; i < 50; i++ {
		if got := hashConfigMap(cm(many)); got != first {
			t.Fatalf("digest is not stable: %s != %s", got, first)
		}
	}
}

// Keys are length-prefixed so no two different maps can serialise identically.
func TestHashConfigMapSeparatesKeysFromValues(t *testing.T) {
	a := hashConfigMap(cm(map[string]string{"ab": "c"}))
	b := hashConfigMap(cm(map[string]string{"a": "bc"}))
	if a == b {
		t.Error("key/value boundaries must be part of the digest")
	}
}

func TestDeploymentCarriesTheConfigHash(t *testing.T) {
	d := &dross.Dross{
		ObjectMeta: metav1.ObjectMeta{Name: "dross-web", Namespace: "dross-system"},
		Spec:       dross.DrossSpec{Image: "img", ConfigMap: "dross-web-config"},
	}
	got := deploymentFor(d, "abc123").Spec.Template.Annotations[configHashAnnotation]
	if got != "abc123" {
		t.Errorf("annotation = %q, want the digest", got)
	}

	// No config, no annotation — so introducing this does not roll a
	// deployment that has nothing to hash.
	plain := deploymentFor(&dross.Dross{
		ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "n"},
		Spec:       dross.DrossSpec{Image: "img"},
	}, "")
	if plain.Spec.Template.Annotations != nil {
		t.Errorf("annotations = %v, want none", plain.Spec.Template.Annotations)
	}
}
