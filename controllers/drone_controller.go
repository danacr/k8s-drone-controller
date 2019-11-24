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

	experimentsv1 "github.com/danacr/drone/api/v1"
)

// DroneReconciler reconciles a Drone object
type DroneReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=experiments.mad.md,resources=drones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=experiments.mad.md,resources=drones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=nodes;pods,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile stuff
func (r *DroneReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("Drone", req.NamespacedName)

	// your logic here
	log.Info("fetching Drone resource")
	Drone := experimentsv1.Drone{}
	if err := r.Client.Get(ctx, req.NamespacedName, &Drone); err != nil {
		log.Error(err, "failed to get Drone resource")
		// Ignore NotFound errors as they will be retried automatically if the
		// resource is created in future.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("checking if we have an existing drone")
	pod := core.Pod{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: Drone.Namespace, Name: Drone.Name}, &pod)
	if apierrors.IsNotFound(err) {
		log.Info("could not find existing Drone, trying to create one...")

		// get list of available nodes that are drones
		dronenodes := core.NodeList{}
		if err := r.List(ctx, &dronenodes, client.MatchingLabels{"node-role.kubernetes.io/drone": "drone"}); err != nil {
			return ctrl.Result{}, err
		}
		// get list of running pods
		dronepods := core.PodList{}
		if err := r.List(ctx, &dronepods, client.InNamespace(Drone.Namespace)); err != nil {
			return ctrl.Result{}, err
		}

		var dronePodNodeNameList []string
		for _, p := range dronepods.Items {
			dronePodNodeNameList = append(dronePodNodeNameList, p.Spec.NodeName)
		}

		for _, dronenode := range dronenodes.Items {
			if !stringInSlice(dronenode.Name, dronePodNodeNameList) {

				// if the node is free, schedule a drone-pod
				pod = *buildPod(Drone, dronenode.Name)
				if err := r.Client.Create(ctx, &pod); err != nil {
					log.Error(err, "failed to create drone")
					return ctrl.Result{}, err
				}

				log.Info("created Drone")
				log.Info("updating Drone resource status")
				Drone.Status.Flying = true
				if r.Update(ctx, &Drone); err != nil {
					log.Error(err, "failed to update Drone")
					return ctrl.Result{}, err
				}

			} else {
				log.Error(err, "Not enough drone nodes")
				Drone.Status.Flying = false
				if r.Update(ctx, &Drone); err != nil {
					log.Error(err, "failed to update Drone")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}

			return ctrl.Result{}, nil
		}

	}
	if err != nil {
		log.Error(err, "failed to get Drone resource")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func buildPod(Drone experimentsv1.Drone, dronenodename string) *core.Pod {
	pod := core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            Drone.Name,
			Namespace:       Drone.Namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&Drone, experimentsv1.GroupVersion.WithKind("Drone"))},
		},
		Spec: core.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": dronenodename,
			},
			Containers: []core.Container{
				{
					Name:  "drone-pod",
					Image: "danacr/drone-pod:latest",
					Env: []core.EnvVar{
						core.EnvVar{Name: "NODE",
							ValueFrom: &core.EnvVarSource{
								FieldRef: &core.ObjectFieldSelector{
									APIVersion: "v1",
									FieldPath:  "spec.nodeName",
								},
							},
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
		if owner.APIVersion != experimentsv1.GroupVersion.String() || owner.Kind != "Drone" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&experimentsv1.Drone{}).
		Owns(&core.Pod{}).
		Complete(r)
}
func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}
