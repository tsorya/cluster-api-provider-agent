/*
Copyright 2021.

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
	"fmt"
	"strings"

	hiveext "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	capiproviderv1alpha1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/openshift/hive/apis/hive/v1/agent"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/cluster-api/controllers/external"
	clusterutilv1 "sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// AgentClusterReconciler reconciles a AgentCluster object
type AgentClusterReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Log             logrus.FieldLogger
	externalTracker external.ObjectTracker
}

//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentclusters/finalizers,verbs=update
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterdeployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=extensions.hive.openshift.io,resources=agentclusterinstalls,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedcontrolplanes,verbs=get;list;watch;

func (r *AgentClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithFields(
		logrus.Fields{
			"agent_cluster":           req.Name,
			"agent_cluster_namespace": req.Namespace,
		})

	defer func() {
		log.Info("AgentCluster Reconcile ended")
	}()

	agentCluster := &capiproviderv1alpha1.AgentCluster{}
	if err := r.Get(ctx, req.NamespacedName, agentCluster); err != nil {
		log.WithError(err).Errorf("Failed to get agentCluster %s", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// If the agentCluster has no reference to a ClusterDeployment, create one
	if agentCluster.Status.ClusterDeploymentRef.Name == "" {
		return r.createClusterDeployment(ctx, log, agentCluster)
	}
	clusterDeployment := &hivev1.ClusterDeployment{}
	err := r.Get(ctx, types.NamespacedName{Namespace: agentCluster.Status.ClusterDeploymentRef.Namespace, Name: agentCluster.Status.ClusterDeploymentRef.Name}, clusterDeployment)
	if err != nil {
		log.WithError(err).Error("Failed to get ClusterDeployment")
		return ctrl.Result{Requeue: true}, err
	}

	result, err := r.ensureAgentClusterInstall(ctx, log, clusterDeployment)
	if err != nil {
		return result, err
	}

	result, err = r.updateAgentClusterInstall(ctx, log, agentCluster, clusterDeployment)
	if err != nil {
		return result, err
	}
	if !agentCluster.Spec.ControlPlaneEndpoint.IsValid() {
		log.Info("Waiting for agentCluster controlPlaneEndpoint")
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, nil
	}
	// If the agentCluster has references a ClusterDeployment, sync from its status
	return r.updateClusterStatus(ctx, log, agentCluster)
}

func (r *AgentClusterReconciler) updateAgentClusterInstall(ctx context.Context, log logrus.FieldLogger, agentCluster *capiproviderv1alpha1.AgentCluster, clusterDeployment *hivev1.ClusterDeployment) (ctrl.Result, error) {
	agentClusterInstall := &hiveext.AgentClusterInstall{}
	err := r.Get(ctx, types.NamespacedName{Namespace: agentCluster.Status.ClusterDeploymentRef.Namespace, Name: clusterDeployment.Spec.ClusterInstallRef.Name}, agentClusterInstall)
	if err != nil {
		log.WithError(err).Error("Failed to get AgentClusterInstall")
		return ctrl.Result{Requeue: true}, err
	}
	updateACI := false

	if agentClusterInstall.Spec.ProvisionRequirements.ControlPlaneAgents < 1 {
		agentClusterInstall.Spec.ProvisionRequirements.ControlPlaneAgents = 3
		updateACI = true
	}

	if agentClusterInstall.Spec.IgnitionEndpoint == nil && agentCluster.Spec.IgnitionEndpoint != nil {
		log.Info("Updating ignition endpoint")
		url := agentCluster.Spec.IgnitionEndpoint.Url
		agentClusterInstall.Spec.IgnitionEndpoint = &hiveext.IgnitionEndpoint{
			// Currently assume something like https://1.2.3.4:555/ignition, otherwise this will fail
			// TODO: Replace with something more robust
			Url: url[0:strings.LastIndex(url, "/")],
		}
		if agentCluster.Spec.IgnitionEndpoint.CaCertificateReference != nil {
			agentClusterInstall.Spec.IgnitionEndpoint.CaCertificateReference = &hiveext.CaCertificateReference{
				Namespace: agentCluster.Spec.IgnitionEndpoint.CaCertificateReference.Namespace,
				Name:      agentCluster.Spec.IgnitionEndpoint.CaCertificateReference.Name,
			}
		}
		updateACI = true
	}

	if updateACI {
		if err = r.Client.Update(ctx, agentClusterInstall); err != nil {
			log.WithError(err).Error("Failed to update agentClusterInstall")
			return ctrl.Result{Requeue: true}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *AgentClusterReconciler) getUnstructuredControlPlane(ctx context.Context, log logrus.FieldLogger,
	agentCluster *capiproviderv1alpha1.AgentCluster) (*unstructured.Unstructured, error) {

	log.Info("Getting control plane")
	// Fetch the CAPI Cluster.
	cluster, err := clusterutilv1.GetOwnerCluster(ctx, r.Client, agentCluster.ObjectMeta)
	if err != nil {
		return nil, err
	}
	if cluster == nil {
		log.Infof("Waiting for Cluster Controller to set OwnerRef on AgentCluster %s %s",
			agentCluster.Name, agentCluster.Namespace)
		return nil, nil
	}

	fmt.Println("AAAAAAAAAAAA", cluster)

	if cluster.Spec.ControlPlaneRef == nil {
		log.Info("Waiting for Cluster to have OwnerRef on Control Plane for AgentCluster %s %s", agentCluster.Name, agentCluster.Namespace)
		return nil, nil
	}

	obj := clusterutilv1.ObjectReferenceToUnstructured(*cluster.Spec.ControlPlaneRef)
	key := client.ObjectKey{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	if err = r.Client.Get(ctx, key, obj); err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve %s external object %q/%q", obj.GetKind(), key.Namespace, key.Name)
	}

	fmt.Println("AAAAAAAAAAAA", obj)

	return obj, nil
}

func (r *AgentClusterReconciler) addControlPlaneWatcher(ctx context.Context, log logrus.FieldLogger, controlPlane *unstructured.Unstructured) error {
	// mapping control plane and agentcluster install through cluster
	mapAgentClusterToControlPlane := func(a client.Object) []reconcile.Request {
		meta := metav1.ObjectMeta{OwnerReferences: a.GetOwnerReferences(), Namespace: a.GetNamespace()}
		cluster, err := clusterutilv1.GetOwnerCluster(ctx, r.Client, meta)
		if err != nil {
			log.WithError(err).Errorf("Failed to get cluster for control plane %s in %s, "+
				"owner ref: %v", a.GetName(), a.GetNamespace(), a.GetOwnerReferences())
			return []reconcile.Request{}
		}

		return []reconcile.Request{{NamespacedName: types.NamespacedName{
			Namespace: cluster.Spec.InfrastructureRef.Namespace,
			Name:      cluster.Spec.InfrastructureRef.Name,
		}}}
	}

	// Ensure we add a watcher to the control plane object.
	// If watcher exists already, it will not be added twice
	if err := r.externalTracker.Watch(ctrl.Log, controlPlane,
		handler.EnqueueRequestsFromMapFunc(mapAgentClusterToControlPlane)); err != nil {
		log.WithError(err).Error("Failed to start control plane watcher")
		return err
	}

	return nil
}

func (r *AgentClusterReconciler) createClusterDeploymentObject(log logrus.FieldLogger, agentCluster *capiproviderv1alpha1.AgentCluster,
	controlPlane *unstructured.Unstructured) (*hivev1.ClusterDeployment, error) {

	var err error

	clusterDeployment := &hivev1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentCluster.Name,
			Namespace: agentCluster.Namespace,
		},
		Spec: hivev1.ClusterDeploymentSpec{
			Installed:   true,
			ClusterName: agentCluster.Spec.ClusterName,
			Platform: hivev1.Platform{
				AgentBareMetal: &agent.BareMetalPlatform{},
			},
			ClusterInstallRef: &hivev1.ClusterInstallLocalReference{
				Kind:    "AgentClusterInstall",
				Group:   hiveext.Group,
				Version: hiveext.Version,
				Name:    agentCluster.Name,
			},
		},
	}

	var ok bool
	clusterDeployment.Spec.BaseDomain, ok, err = unstructured.NestedString(controlPlane.UnstructuredContent(), "spec", "dns", "baseDomain")
	if !ok {
		log.Infof("Control plane object has no baseDomain, waiting more")
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to get base domain")
	}

	pullSecretName, ok, err := unstructured.NestedString(controlPlane.UnstructuredContent(), "spec", "pullSecret", "name")
	if !ok {
		log.Infof("Control plane object has no pullSecretName, waiting more")
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to get pull secret ref")
	}
	clusterDeployment.Spec.PullSecretRef = &corev1.LocalObjectReference{Name: pullSecretName}

	return clusterDeployment, nil
}

func (r *AgentClusterReconciler) createClusterDeployment(ctx context.Context, log logrus.FieldLogger, agentCluster *capiproviderv1alpha1.AgentCluster) (ctrl.Result, error) {
	fmt.Println("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	controlPlane, err := r.getUnstructuredControlPlane(ctx, log, agentCluster)
	if controlPlane == nil {
		result := ctrl.Result{}
		if err != nil {
			result.Requeue = true
		}
		return result, err
	}

	// Ensure we add a watcher to the control plane object.
	err = r.addControlPlaneWatcher(ctx, log, controlPlane)
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	log.Info("Creating clusterDeployment")
	clusterDeployment, err := r.createClusterDeploymentObject(log, agentCluster, controlPlane)
	if clusterDeployment == nil {
		return ctrl.Result{Requeue: true}, err
	}

	agentCluster.Status.ClusterDeploymentRef.Name = clusterDeployment.Name
	agentCluster.Status.ClusterDeploymentRef.Namespace = clusterDeployment.Namespace
	if err := r.Client.Create(ctx, clusterDeployment); err != nil {
		log.WithError(err).Error("Failed to create ClusterDeployment")
		return ctrl.Result{Requeue: true}, nil
	}
	if err := r.Client.Status().Update(ctx, agentCluster); err != nil {
		log.WithError(err).Error("Failed to update status")
		return ctrl.Result{Requeue: true}, err
	}
	return ctrl.Result{}, nil
}

func (r *AgentClusterReconciler) ensureAgentClusterInstall(ctx context.Context, log logrus.FieldLogger, clusterDeployment *hivev1.ClusterDeployment) (ctrl.Result, error) {
	log.Info("Setting AgentClusterInstall")
	agentClusterInstall := &hiveext.AgentClusterInstall{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: clusterDeployment.Namespace, Name: clusterDeployment.Name}, agentClusterInstall); err != nil {
		if apierrors.IsNotFound(err) {
			err = r.createAgentClusterInstall(ctx, log, clusterDeployment)
			if err != nil {
				log.WithError(err).Error("failed to create AgentClusterInstall")
				return ctrl.Result{Requeue: true}, err
			}
		} else {
			log.WithError(err).Error("Failed to get AgentClusterInstall")
			return ctrl.Result{Requeue: true}, err
		}
	}
	return ctrl.Result{Requeue: true}, nil
}

func (r *AgentClusterReconciler) createAgentClusterInstall(ctx context.Context, log logrus.FieldLogger, clusterDeployment *hivev1.ClusterDeployment) error {
	log.Infof("Creating AgentClusterInstall for clusterDeployment: %s %s", clusterDeployment.Namespace, clusterDeployment.Name)
	agentClusterInstall := &hiveext.AgentClusterInstall{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterDeployment.Name,
			Namespace: clusterDeployment.Namespace,
		},
		Spec: hiveext.AgentClusterInstallSpec{
			ClusterDeploymentRef: corev1.LocalObjectReference{Name: clusterDeployment.Name},
		},
	}
	return r.Client.Create(ctx, agentClusterInstall)
}

func (r *AgentClusterReconciler) updateClusterStatus(ctx context.Context, log logrus.FieldLogger, agentCluster *capiproviderv1alpha1.AgentCluster) (ctrl.Result, error) {
	log.Infof("Updating agentCluster status according to %s", agentCluster.Status.ClusterDeploymentRef.Name)
	// Once the cluster have clusterDeploymentRef and ClusterInstallRef we should set the status to Ready
	agentCluster.Status.Ready = true
	if err := r.Status().Update(ctx, agentCluster); err != nil {
		log.WithError(err).Error("Failed to set ready status")
		return ctrl.Result{Requeue: true}, nil

	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controller, err := ctrl.NewControllerManagedBy(mgr).
		For(&capiproviderv1alpha1.AgentCluster{}).
		Build(r)

	r.externalTracker = external.ObjectTracker{
		Controller: controller,
	}

	return err
}
