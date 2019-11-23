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
	apps "k8s.io/api/apps/v1"
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
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;delete
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
		log.Error(err, "failed to clean up old Deployment resources for this Drone")
		return ctrl.Result{}, err
	}

	log.Info("checking if an existing Deployment exists for this resource")
	deployment := apps.Deployment{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: Drone.Namespace, Name: "mydrones"}, &deployment)
	if apierrors.IsNotFound(err) {
		log.Info("could not find existing Deployment for Drone, creating one...")

		deployment = *buildDeployment(Drone)
		if err := r.Client.Create(ctx, &deployment); err != nil {
			log.Error(err, "failed to create Deployment resource")
			return ctrl.Result{}, err
		}

		log.Info("created Deployment resource for Drone")
		return ctrl.Result{}, nil
	}
	if err != nil {
		log.Error(err, "failed to get Deployment for Drone resource")
		return ctrl.Result{}, err
	}

	log.Info("existing Deployment resource already exists for Drone, checking replica count")

	expectedReplicas := int32(1)
	if Drone.Spec.HowMany != nil {
		expectedReplicas = *Drone.Spec.HowMany
	}
	if *deployment.Spec.Replicas != expectedReplicas {
		log.Info("updating replica count", "old_count", *deployment.Spec.Replicas, "new_count", expectedReplicas)

		deployment.Spec.Replicas = &expectedReplicas
		if err := r.Client.Update(ctx, &deployment); err != nil {
			log.Error(err, "failed to Deployment update replica count")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	log.Info("replica count up to date", "replica_count", *deployment.Spec.Replicas)

	log.Info("updating Drone resource status")
	Drone.Status.FlyingDrones = deployment.Status.Replicas
	if r.Client.Status().Update(ctx, &Drone); err != nil {
		log.Error(err, "failed to update Drone status")
		return ctrl.Result{}, err
	}

	log.Info("resource status synced")

	return ctrl.Result{}, nil
}

// cleanupOwnedResources will Delete any existing Deployment resources that
// were created for the given Drone that no longer match the
// Drone.spec.deploymentName field.
func (r *DroneReconciler) cleanupOwnedResources(ctx context.Context, log logr.Logger, Drone *madmdv1.Drone) error {
	log.Info("finding existing Deployments for Drone resource")

	// List all deployment resources owned by this Drone
	var deployments apps.DeploymentList
	if err := r.List(ctx, &deployments, client.InNamespace(Drone.Namespace), client.MatchingField(deploymentOwnerKey, Drone.Name)); err != nil {
		return err
	}

	deleted := 0
	for _, depl := range deployments.Items {
		if depl.Name == "mydrones" {
			// If this deployment's name matches the one on the Drone resource
			// then do not delete it.
			continue
		}

		if err := r.Client.Delete(ctx, &depl); err != nil {
			log.Error(err, "failed to delete Deployment resource")
			return err
		}

		deleted++
	}

	log.Info("finished cleaning up old Deployment resources", "number_deleted", deleted)

	return nil
}

func buildDeployment(Drone madmdv1.Drone) *apps.Deployment {
	deployment := apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "mydrones",
			Namespace:       Drone.Namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&Drone, madmdv1.GroupVersion.WithKind("Drone"))},
		},
		Spec: apps.DeploymentSpec{
			Replicas: Drone.Spec.HowMany,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"example-controller.jetstack.io/deployment-name": "mydrones",
				},
			},
			Template: core.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"example-controller.jetstack.io/deployment-name": "mydrones",
					},
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
			},
		},
	}
	return &deployment
}

var (
	deploymentOwnerKey = ".metadata.controller"
)

// SetupWithManager stuff
func (r *DroneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(&apps.Deployment{}, deploymentOwnerKey, func(rawObj runtime.Object) []string {
		// grab the Deployment object, extract the owner...
		depl := rawObj.(*apps.Deployment)
		owner := metav1.GetControllerOf(depl)
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
		Owns(&apps.Deployment{}).
		Complete(r)
}
