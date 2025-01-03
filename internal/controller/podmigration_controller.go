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
	"github.com/go-logr/logr"
	migrationv1 "k8s-checkpoint-controller/api/v1"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var MigrationLabel = "kubernetes.io/associated-migration"
var SourcePodLabel = "kubernetes.io/source-pod"
var SourceNamespaceLabel = "kubernetes.io/source-namespace"

var phaseMapper = map[corev1.PodPhase]migrationv1.PodMigrationPhase{
	corev1.PodPending:   migrationv1.Restoring,
	corev1.PodRunning:   migrationv1.Succeeded,
	corev1.PodSucceeded: migrationv1.Succeeded,
	corev1.PodFailed:    migrationv1.Failed,
	corev1.PodUnknown:   migrationv1.Unknown,
}

// PodMigrationReconciler reconciles a PodMigration object
type PodMigrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *PodMigrationReconciler) getMigrationInfo(ctx context.Context, req ctrl.Request, logs logr.Logger) (migrationv1.PodMigration, corev1.Pod, error) {
	var migration migrationv1.PodMigration
	if err := r.Get(ctx, req.NamespacedName, &migration); err != nil {
		logs.Error(err, "unable to fetch Migration")
		return migrationv1.PodMigration{}, corev1.Pod{}, client.IgnoreNotFound(err)
	}

	var sourcePod corev1.Pod
	podNamespacedName := types.NamespacedName{
		Namespace: migration.Namespace,
		Name:      migration.Spec.PodName,
	}

	if err := r.Get(ctx, podNamespacedName, &sourcePod); err != nil {
		logs.Error(err, "unable to fetch source Pod")
		return migrationv1.PodMigration{}, corev1.Pod{}, err
	}

	return migration, sourcePod, nil
}

func (r *PodMigrationReconciler) createRestoredPodSpec(sourcePod corev1.Pod, migration migrationv1.PodMigration) corev1.Pod {

	// TODO: Did we copy all the relevant fields?
	var restoredPod corev1.Pod
	restoredPod.Spec = sourcePod.Spec
	restoredPod.Annotations = sourcePod.Annotations

	restoredPod.Name = migration.Spec.NewPodName
	restoredPod.Namespace = migration.Namespace
	restoredPod.Spec.NodeName = migration.Spec.NodeName
	restoredPod.Spec.NodeSelector = migration.Spec.NodeSelector

	if restoredPod.Annotations == nil {
		restoredPod.Annotations = map[string]string{}
	}

	restoredPod.Annotations[MigrationLabel] = migration.Name
	restoredPod.Annotations[SourcePodLabel] = sourcePod.Name
	restoredPod.Annotations[SourceNamespaceLabel] = sourcePod.Namespace

	return restoredPod
}

// TODO: implement proper reconciliation logic
func (r *PodMigrationReconciler) restorePod(ctx context.Context, req ctrl.Request, logs logr.Logger) (*migrationv1.PodMigration, *corev1.PodPhase, error) {
	// what if source pod is gone already?
	migration, sourcePod, err := r.getMigrationInfo(ctx, req, logs)
	if err != nil {
		logs.Error(err, "failed to get Migration info")
		return nil, nil, err
	}

	restoredPod := r.createRestoredPodSpec(sourcePod, migration)
	if err = r.Create(ctx, &restoredPod); err != nil && !apierrors.IsAlreadyExists(err) {
		logs.Error(err, "failed to create restored Pod")
		return nil, nil, err
	}

	restoredPodNamespacedName := types.NamespacedName{
		Namespace: restoredPod.Namespace,
		Name:      restoredPod.Name,
	}

	if err = r.Get(ctx, restoredPodNamespacedName, &restoredPod); err != nil {
		logs.Error(err, "unable to fetch the restored Pod")
		return nil, nil, err
	}

	status := restoredPod.Status.Phase

	// Delete the old Pod
	if status == corev1.PodRunning || status == corev1.PodSucceeded {
		if err = r.Get(ctx, restoredPodNamespacedName, &sourcePod); err != nil {
			// if pod is deleted already, then it is ok.
			if apierrors.IsNotFound(err) {
				return &migration, &status, nil
			}

			return nil, nil, err
		}
		if err = r.Delete(ctx, &sourcePod); err != nil {
			return nil, nil, err
		}
	}

	return &migration, &status, nil
}

// +kubebuilder:rbac:groups=migration.k8s-checkpoint-controller,resources=podmigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migration.k8s-checkpoint-controller,resources=podmigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migration.k8s-checkpoint-controller,resources=podmigrations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *PodMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logs := log.FromContext(ctx)
	migration, status, err := r.restorePod(ctx, req, logs)
	result := ctrl.Result{}

	// TODO:
	// - Decide on namespace behaviour?
	// - Add source pod, namespace, destination node and status when viewed by kubectl

	if migration == nil {
		result.RequeueAfter = time.Second * 5
		return result, err
	} else if err != nil {
		migration.Status.Phase = migrationv1.Failed
	} else {
		migration.Status.Phase = phaseMapper[*status]
	}

	if migration.Status.Phase != migrationv1.Succeeded {
		result.RequeueAfter = time.Second * 5
	}

	if err = r.Status().Update(ctx, migration); err != nil {
		logs.Error(err, "unable to update Migration status")
		return result, err
	}

	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&migrationv1.PodMigration{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				if m, ok := obj.GetLabels()[MigrationLabel]; ok {
					return []reconcile.Request{
						{
							NamespacedName: types.NamespacedName{
								Name:      m,
								Namespace: obj.GetNamespace(),
							},
						},
					}
				}
				// If the label is not present or doesn't match, don't trigger reconciliation
				return []reconcile.Request{}
			}),
		).
		Complete(r)
}
