package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dross "github.com/dougkirkley/dross/api/v1alpha1"
)

// statefulSetFor builds the workload that runs one session.
//
// A StatefulSet rather than a bare pod because a session has to survive a
// restart. A pod has no controller behind it: once the node reboots or the
// kubelet evicts it, it is gone. A StatefulSet brings the session back with the
// same name and — through volumeClaimTemplates — the same volume, which is what
// makes the workspace persistent rather than merely long-lived.
//
// It also makes pausing free: scale to 0 and an idle session stops consuming
// CPU and memory while keeping its files; scale back to 1 and it resumes.
func statefulSetFor(s *dross.Session) *appsv1.StatefulSet {
	one := int32(1)
	size := s.Spec.Storage
	if size == "" {
		size = "5Gi"
	}
	qty, err := resource.ParseQuantity(size)
	if err != nil {
		qty = resource.MustParse("5Gi")
	}

	sc := s.Spec.StorageClass

	set := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: s.Namespace,
			Name:      s.Name,
			Labels:    podLabelsFor(s),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &one,
			// Explicit, though it is also the default: an image change to this
			// workload replaces the pod by itself, rather than waiting for
			// someone to delete it. A StatefulSet takes the old pod down before
			// bringing the new one up, which is what the workspace volume needs
			// — it is ReadWriteOnce and cannot be attached to both at once.
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
			ServiceName: s.Name,
			Selector:    &metav1.LabelSelector{MatchLabels: labelsFor(s)},
			Template:    podTemplateFor(s),
			// Delete the volume with the session. Kubernetes retains
			// volumeClaimTemplate PVCs by default, which would leak a disk for
			// every session ever created.
			PersistentVolumeClaimRetentionPolicy: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenDeleted: appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
				WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "workspace"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: qty},
					},
				},
			}},
		},
	}
	if sc != "" {
		set.Spec.VolumeClaimTemplates[0].Spec.StorageClassName = &sc
	}
	return set
}
