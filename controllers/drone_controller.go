/*
Copyright 2019 The Kubernetes Authors.

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

package controllers

import (
	"context"

	"github.com/go-logr/logr"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	madmdv1 "github.com/danacr/drone/api/v1"
)

// DroneReconciler reconciles a Drone object
type DroneReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=experiments.mad.md,resources=drones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=experiments.mad.md,resources=drones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile stuff
func (r *DroneReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("Drone", req.NamespacedName)

	// your logic here
	log.Info("fetching Drone resource")
	Drone := madmdv1.Drone{}
	if err := r.Client.Get(ctx, req.NamespacedName, &Drone); err != nil {
		log.Error(err, "failed to get Drone resource")
		// Ignore NotFound errors as they will be retried automatically if the
		// resource is created in future.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.cleanupOwnedResources(ctx, log, &Drone); err != nil {
		log.Error(err, "failed to delete old drones")
		return ctrl.Result{}, err
	}

	log.Info("checking if we have an existing drone")
	pod := core.Pod{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: Drone.Namespace, Name: Drone.Spec.Name}, &pod)
	if apierrors.IsNotFound(err) {
		log.Info("could not find existing Drone, creating one...")

		pod = *buildPod(Drone)
		if err := r.Client.Create(ctx, &pod); err != nil {
			log.Error(err, "failed to create drone")
			return ctrl.Result{}, err
		}

		log.Info("created Drone")
		return ctrl.Result{}, nil
	}
	if err != nil {
		log.Error(err, "failed to get Drone resource")
		return ctrl.Result{}, err
	}

	log.Info("updating Drone resource status")
	Drone.Status.Name = pod.Name
	if r.Client.Status().Update(ctx, &Drone); err != nil {
		log.Error(err, "failed to update Drone name")
		return ctrl.Result{}, err
	}

	log.Info("resource status synced")

	return ctrl.Result{}, nil
}

// cleanupOwnedResources will Delete any existing pod resources that
// were created for the given Drone that no longer match the
// Drone.spec.Name field.
func (r *DroneReconciler) cleanupOwnedResources(ctx context.Context, log logr.Logger, Drone *madmdv1.Drone) error {
	log.Info("finding existing pod for Drone resource")

	// List all pods resources owned by this Drone
	var pods core.PodList
	if err := r.List(ctx, &pods, client.InNamespace(Drone.Namespace), client.MatchingField(podOwnerKey, Drone.Name)); err != nil {
		return err
	}

	deleted := 0
	for _, po := range pods.Items {
		if po.Name == Drone.Spec.Name {
			// If this pod's name matches the one on the Drone resource
			// then do not delete it.
			continue
		}

		if err := r.Client.Delete(ctx, &po); err != nil {
			log.Error(err, "failed to delete Drone resource")
			return err
		}

		deleted++
	}

	log.Info("finished cleaning up old Drones resources", "number_deleted", deleted)

	return nil
}

func buildPod(Drone madmdv1.Drone) *core.Pod {
	pod := core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            Drone.Spec.Name,
			Namespace:       Drone.Namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&Drone, madmdv1.GroupVersion.WithKind("Drone"))},
		},
		Spec: core.PodSpec{
			Containers: []core.Container{
				{
					Name:  "drone-pod",
					Image: "danacr/drone-pod:latest",
					Env: []core.EnvVar{
						core.EnvVar{Name: "NODE",
							Value: "rockpi0",
						},
					},
				},
			},
		},
	}
	return &pod
}

var (
	podOwnerKey = ".metadata.controller"
)

// SetupWithManager stuff
func (r *DroneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(&core.Pod{}, podOwnerKey, func(rawObj runtime.Object) []string {
		// grab the Deployment object, extract the owner...
		po := rawObj.(*core.Pod)
		owner := metav1.GetControllerOf(po)
		if owner == nil {
			return nil
		}
		// ...make sure it's a Drone...
		if owner.APIVersion != madmdv1.GroupVersion.String() || owner.Kind != "Drone" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&madmdv1.Drone{}).
		Owns(&core.Pod{}).
		Complete(r)
}
