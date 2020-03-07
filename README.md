### A Cloud Native Drone Management System

[![Build Status](https://cloud.drone.io/api/badges/danacr/k8s-drone-controller/status.svg)](https://cloud.drone.io/danacr/k8s-drone-controller)

Kubernetes operators are controllers that manage custom resources. In our case, we will implement a simple one where we have a Swarm Resource that is composed of individual Drones.

I strongly recommend reading the [The Kubebuilder book](https://book.kubebuilder.io/quick-start.html) as well as checking out the [Jetstack Kubebuilder Sample Controller](https://github.com/jetstack/kubebuilder-sample-controller)

Let's start by declaring our custom Drone type:

```go
// DroneSpec defines the desired state of Drone
type DroneSpec struct {
}

// DroneStatus defines the observed state of Drone
type DroneStatus struct {
	Flying bool `json:"flying,omitempty"`
}
```

> Note: The drone does not have any specification, but it will generate a status regarding whether it is flying or not.

Once we have a drone specification, we can build the drone controller:

```go
func (r *DroneReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("Drone", req.NamespacedName)

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
```

> Note: Kubebuilder does a significant amount of scaffolding for you, so one has to simply add his custom reconcile logic for the controllers.

In order to launch the drone, we need to make sure that we schedule the pod on the correct node, and this will start the [drone-pod container](https://github.com/danacr/drone-pod) using the correct environment variables.

```go
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
```

Since our controller knows what a drone is, the only way to start them now is by manually requesting drone, but what if we could use an aggregator of some sorts?

This is why I chose to create the Swarm resource and controller:

```go
// SwarmSpec defines the desired state of Swarm
type SwarmSpec struct {
	HowMany *int32 `json:"howmany,omitempty"`
}

// SwarmStatus defines the observed state of Swarm
type SwarmStatus struct {
	FlyingDrones int32 `json:"flyingdrones,omitempty"`
}
```

> Note: The Swarm Custom Resource allows us to request a number of drones to be launched and to observe that state using the Status field

The swarm controller simply checks for the current number of drones and launches new ones or lands existing ones.

```go
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
```

> Note: There appears to be a bug at the moment which causes drones to refuse flying on the first try, so the drone-pod container has to be rescheduled in order to start the drone.

Once this operator is deployed on the cluster, you can request Drones from Kubernetes the same way you would request pods :)
