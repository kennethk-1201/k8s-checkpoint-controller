/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"net/http"

	migrationv1 "k8s-checkpoint-controller/api/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PodMigrationReconciler reconciles a PodMigration object
type PodMigrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *PodMigrationReconciler) GetInfo(ctx context.Context, req ctrl.Request) (*migrationv1.PodMigration, *corev1.Pod, *corev1.Node, error) {
	l := log.FromContext(ctx)
	var migration migrationv1.PodMigration

	if err := r.Get(ctx, req.NamespacedName, &migration); err != nil {
		return nil, nil, nil, client.IgnoreNotFound(err)
	}

	l.Info("Migration", "Name", migration.Name, "Namespace", migration.Namespace)

	var pod corev1.Pod
	podNamespacedName := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      migration.Spec.PodName,
	}

	if err := r.Get(ctx, podNamespacedName, &pod); err != nil {
		return nil, nil, nil, client.IgnoreNotFound(err)
	}

	l.Info("Pod", "Name", pod.Name, "Namespace", pod.Namespace)

	var destNode corev1.Node
	nodeNamespacedName := types.NamespacedName{
		Name: migration.Spec.NodeName,
	}

	if err := r.Get(ctx, nodeNamespacedName, &destNode); err != nil {
		return nil, nil, nil, client.IgnoreNotFound(err)
	}

	l.Info("Destination Node", "Name", destNode.Name)

	return &migration, &pod, &destNode, nil
}

func (r *PodMigrationReconciler) IsPodRestored(ctx context.Context, pod *corev1.Pod) (bool, error) {
	migratedPodNamespaceName := types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name + "-restored",
	}

	var migratedPod corev1.Pod

	if err := r.Get(ctx, migratedPodNamespaceName, &migratedPod); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	return migratedPod.Status.Phase == corev1.PodSucceeded, nil
}

// +kubebuilder:rbac:groups=migration.k8s-checkpoint-controller,resources=podmigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migration.k8s-checkpoint-controller,resources=podmigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migration.k8s-checkpoint-controller,resources=podmigrations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PodMigration object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *PodMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	_ = log.FromContext(ctx)
	API_SERVER := "kubernetes.default.svc.cluster.local"

	// 1. Get the necessary information
	migration, pod, destNode, err := r.GetInfo(ctx, req)

	if err != nil {
		return ctrl.Result{}, err
	}

	if migration.Status.Phase == "" {
		migration.Status.Phase = migrationv1.Pending
	}

	switch migration.Status.Phase {
	case migrationv1.Pending:
		// This request is sent to the Kubelet via the API server using its in-built proxy path.
		// The /migrate endpoint will trigger an asynchronous migration process in the source node.
		// TODO: complete the endpoint
		endpoint := fmt.Sprintf("http://%s/api/v1/nodes/%s/proxy/migrate/%s/%s", API_SERVER, pod.Spec.NodeName, pod.Namespace, pod.Name)
		_, err = http.Post(endpoint, "application/json", nil)

		if err != nil {
			return ctrl.Result{}, err
		}

		migration.Status.Phase = migrationv1.Migrating

		if err := r.Status().Update(ctx, migration); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	case migrationv1.Migrating:
		// Wait for signal by the source node that the checkpoint is done.
		// TODO: Add synchronization to prevent race conditions.
		if val, ok := pod.Annotations["checkpoint.completed"]; ok && val == "done" {
			migration.Status.Phase = migrationv1.Restoring

			if err := r.Status().Update(ctx, migration); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	case migrationv1.Restoring:
		// Restore the Pod and wait for it to successfully start.
		podRestored, err := r.IsPodRestored(ctx, pod)

		if err != nil {
			return ctrl.Result{}, err
		}

		if podRestored {
			migration.Status.Phase = migrationv1.CleaningUp

			if err := r.Status().Update(ctx, migration); err != nil {
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		} else {
			// Create new Pod with a different Pod name and set pod.type = restore.
			var migratedPod corev1.Pod
			copyRelevantFields(pod, &migratedPod)

			migratedPod.Name = pod.Name + "-restored"
			migratedPod.Spec.NodeName = destNode.Name
			migratedPod.Annotations["pod.type"] = "restore"

			if err := r.Create(ctx, &migratedPod); err != nil {
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}
	case migrationv1.CleaningUp:
		// Delete old Pod and update that migration succeeded.
		if err := r.Delete(ctx, pod, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			return ctrl.Result{}, err
		}

		migration.Status.Phase = migrationv1.Succeeded

		if err := r.Status().Update(ctx, migration); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	case migrationv1.Succeeded:
		return ctrl.Result{}, nil
	case migrationv1.Failed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func copyRelevantFields(src *corev1.Pod, dst *corev1.Pod) {
	dst.Namespace = src.Namespace
	dst.Spec.Containers = []corev1.Container{}

	for i := 0; i < len(src.Spec.Containers); i++ {
		var newContainer corev1.Container
		newContainer.Name = src.Spec.Containers[i].Name
		newContainer.Image = src.Spec.Containers[i].Image

		dst.Spec.Containers = append(dst.Spec.Containers, newContainer)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&migrationv1.PodMigration{}).
		Complete(r)
}
