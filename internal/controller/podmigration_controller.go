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

	// 1. Get the necessary information
	migration, pod, destNode, err := r.GetInfo(ctx, req)

	if err != nil {
		return ctrl.Result{}, err
	}

	// 2. Start migration process
	migration.Status.Phase = migrationv1.Running

	if err := r.Status().Update(ctx, migration); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Signal source node to checkpoint, signal to controller when it is done.

	// TODO: Set port to be env variable
	_, err = http.Post(fmt.Sprintf("http://%s:2837/checkpoint/%s/%s", pod.Status.HostIP, pod.Namespace, pod.Name), "application/json", nil)

	if err != nil {
		return ctrl.Result{}, err
	}

	// 4. Signal to destination node to download the checkpoint from source node
	// 5. Destination node builds image and push to some local registry that is accessible by the kubelet in the destination node.
	// 6. Destination node signals to controller when it is done pushing.

	_, err = http.Post(fmt.Sprintf("http://%s:2837/checkpoint/%s/%s", destNode.Status.Addresses[0], pod.Namespace, pod.Name), "application/json", nil)

	if err != nil {
		return ctrl.Result{}, err
	}

	// 7. Delete old Pod.
	if err := r.Delete(ctx, pod, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		return ctrl.Result{}, err
	}

	// 8. Create new Pod with the same Pod name and the new image (along with all the other configuration).
	migratedPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  pod.Spec.Containers[0].Name,
					Image: "", // will be named by some convention
				},
			},
		},
	}

	if err := r.Create(ctx, &migratedPod); err != nil {
		return ctrl.Result{}, err
	}

	// 9. End migration process
	migration.Status.Phase = migrationv1.Succeeded

	if err := r.Status().Update(ctx, migration); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&migrationv1.PodMigration{}).
		Complete(r)
}
