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

func (r *PodMigrationReconciler) getRestoredPodInfo(ctx context.Context, migration migrationv1.PodMigration, logs logr.Logger) (corev1.Pod, error) {
	newPodNamespacedName := types.NamespacedName{
		Namespace: migration.Namespace,
		Name:      migration.Spec.NewPodName,
	}

	var newPod corev1.Pod

	if err := r.Get(ctx, newPodNamespacedName, &newPod); err != nil {
		if !apierrors.IsNotFound(err) {
			logs.Error(err, "unable to check if new Pod is already restored")
		}
		return corev1.Pod{}, err
	}

	return newPod, nil
}

func (r *PodMigrationReconciler) getMigrationInfo(ctx context.Context, req ctrl.Request, logs logr.Logger) (migrationv1.PodMigration, error) {
	var migration migrationv1.PodMigration
	if err := r.Get(ctx, req.NamespacedName, &migration); err != nil {
		logs.Error(err, "unable to fetch Migration")
		return migrationv1.PodMigration{}, client.IgnoreNotFound(err)
	}

	return migration, nil
}

func (r *PodMigrationReconciler) getSourcePodInfo(ctx context.Context, migration migrationv1.PodMigration, logs logr.Logger) (corev1.Pod, error) {
	var sourcePod corev1.Pod
	podNamespacedName := types.NamespacedName{
		Namespace: migration.Namespace,
		Name:      migration.Spec.PodName,
	}

	if err := r.Get(ctx, podNamespacedName, &sourcePod); err != nil {
		logs.Error(err, "unable to fetch source Pod")
		return corev1.Pod{}, err
	}

	return sourcePod, nil
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

	// create annotations for restore logic
	if restoredPod.Annotations == nil {
		restoredPod.Annotations = map[string]string{}
	}

	// for controller
	restoredPod.Annotations[MigrationLabel] = migration.Name

	// for kubelet
	restoredPod.Annotations[SourcePodLabel] = sourcePod.Name
	restoredPod.Annotations[SourceNamespaceLabel] = sourcePod.Namespace

	return restoredPod
}

// The reconciliation loop should guarantee that:
// - the new Pod is restored with the old Pod's state
// - the old Pod is deleted.
func (r *PodMigrationReconciler) migratePod(ctx context.Context, req ctrl.Request, logs logr.Logger) (*migrationv1.PodMigration, *corev1.PodPhase, error) {
	migration, err := r.getMigrationInfo(ctx, req, logs)
	if err != nil {
		logs.Error(err, "failed to get Migration info")
		return nil, nil, err
	}

	if migration.Status.Phase == migrationv1.Succeeded {
		podPhase := corev1.PodSucceeded
		return &migration, &podPhase, nil
	}

	restoredPod, err := r.getRestoredPodInfo(ctx, migration, logs)

	var status corev1.PodPhase
	var sourcePod corev1.Pod
	var sourcePodFound bool // for caching to prevent multiple calls

	if err != nil {
		// if the new Pod is not restored yet, attempt to restore.
		if apierrors.IsNotFound(err) {
			sourcePod, err = r.getSourcePodInfo(ctx, migration, logs)
			if err != nil {
				logs.Error(err, "failed to get the source Pod info")
				return nil, nil, err
			}

			sourcePodFound = true
			restoredPod = r.createRestoredPodSpec(sourcePod, migration)

			// Attempt to create the new Pod. If the Pod already exists, ignore the error as it
			// means that the Pod was already restored.
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
		} else {
			logs.Error(err, "unable to fetch the restored Pod")
			return nil, nil, err
		}
	}

	// at this stage, the restored Pod definitely exists
	status = restoredPod.Status.Phase

	// if the restored Pod is successfully created, we can remove the old Pod
	if status == corev1.PodRunning || status == corev1.PodSucceeded {
		if !sourcePodFound {
			sourcePod, err = r.getSourcePodInfo(ctx, migration, logs)
			if err != nil {
				logs.Error(err, "failed to get the source Pod info")
				return nil, nil, err
			}

			sourcePodFound = true
		}

		if err = r.Delete(ctx, &sourcePod); err != nil && !apierrors.IsNotFound(err) {
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
	migration, status, err := r.migratePod(ctx, req, logs)
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
