/*

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
	"strings"

	experimentsv1 "github.com/danacr/drone/api/v1"
	"github.com/docker/docker/pkg/namesgenerator"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SwarmReconciler reconciles a Swarm object
type SwarmReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=experiments.mad.md,resources=swarms;drones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=experiments.mad.md,resources=swarms/status,verbs=get;update;patch

// Reconcile stuff
func (r *SwarmReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("Swarm", req.NamespacedName)

	// your logic here
	log.Info("fetching swarm resource")
	swarm := experimentsv1.Swarm{}
	if err := r.Client.Get(ctx, req.NamespacedName, &swarm); err != nil {
		log.Error(err, "failed to get swarm")
		// Ignore NotFound errors as they will be retried automatically if the
		// resource is created in future.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Do we have enough drones?")

	drones := experimentsv1.DroneList{}
	if err := r.List(ctx, &drones); err != nil {
		return ctrl.Result{}, err
	}

	if int32(len(drones.Items)) < *swarm.Spec.HowMany {
		log.Info("Not enough, must create drones")

		name := strings.ReplaceAll(namesgenerator.GetRandomName(0), "_", "-")

		drone := experimentsv1.Drone{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: req.Namespace,
			},
		}
		if err := r.Client.Create(ctx, &drone); err != nil {
			log.Error(err, "failed to create drone")
			return ctrl.Result{}, err
		}

	}
	if int32(len(drones.Items)) > *swarm.Spec.HowMany {
		log.Info("Too many, must kill")
		r.Delete(ctx, &experimentsv1.Drone{
			ObjectMeta: ctrl.ObjectMeta{
				Name:      drones.Items[0].Name,
				Namespace: req.Namespace,
			},
		})
	}

	log.Info("updating swarm status")
	if err := r.List(ctx, &drones); err != nil {
		return ctrl.Result{}, err
	}
	swarm.Status.FlyingDrones = int32(len(drones.Items))
	err := r.Update(ctx, &swarm)
	if err != nil {
		log.Error(err, "failed to update swarm status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager stuff
func (r *SwarmReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&experimentsv1.Swarm{}).
		Complete(r)
}
