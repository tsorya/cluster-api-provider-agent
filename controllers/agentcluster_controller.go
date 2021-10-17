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
	capiproviderv1alpha1 "github.com/eranco74/cluster-api-provider-agent/api/v1alpha1"
	hiveext "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	logutil "github.com/openshift/assisted-service/pkg/log"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AgentClusterReconciler reconciles a AgentCluster object
type AgentClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logrus.FieldLogger
}

//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentclusters/finalizers,verbs=update

func (r *AgentClusterReconciler) Reconcile(originalCtx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx := addRequestIdIfNeeded(originalCtx)
	log := logutil.FromContext(ctx, r.Log).WithFields(
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

	// If the agentCluster is ready, we have nothing to do
	if agentCluster.Status.Ready {
		return ctrl.Result{}, nil
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
	if clusterDeployment.Spec.ClusterInstallRef == nil {
		return r.SetAgentClusterInstallRef(ctx, log, clusterDeployment, agentCluster.Spec.ImageSetRef)
	}
	// If the agentCluster has references a ClusterDeployment, sync from its status
	return r.updateClusterStatus(ctx, log, agentCluster)
}

func (r *AgentClusterReconciler) createClusterDeployment(ctx context.Context, log logrus.FieldLogger, agentCluster *capiproviderv1alpha1.AgentCluster) (ctrl.Result, error) {
	log.Info("Creating clusterDeployment")
	clusterDeployment := &hivev1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentCluster.Name,
			Namespace: agentCluster.Namespace,
		},
		Spec: hivev1.ClusterDeploymentSpec{
			Installed:     true,
			BaseDomain:    agentCluster.Spec.BaseDomain,
			ClusterName:   agentCluster.Spec.ClusterName,
			PullSecretRef: agentCluster.Spec.PullSecretRef},
	}
	agentCluster.Status.ClusterDeploymentRef.Name = clusterDeployment.Name
	if err := r.Client.Create(ctx, clusterDeployment); err != nil {
		log.WithError(err).Error("Failed to create ClusterDeployment")
		return ctrl.Result{Requeue: true}, nil
	}
	if err := r.Client.Status().Update(ctx, agentCluster); err != nil {
		log.WithError(err).Error("Failed to update status")
		return ctrl.Result{Requeue: true}, nil
	}
	return ctrl.Result{}, nil
}

func (r *AgentClusterReconciler) SetAgentClusterInstallRef(ctx context.Context, log logrus.FieldLogger, clusterDeployment *hivev1.ClusterDeployment, imageSetRef *hivev1.ClusterImageSetReference) (ctrl.Result, error) {
	log.Info("Setting AgentClusterInstall")
	agentClusterInstall := &hiveext.AgentClusterInstall{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: clusterDeployment.Namespace, Name: clusterDeployment.Name}, agentClusterInstall); err != nil {
		if apierrors.IsNotFound(err) {
			err = r.createAgentClusterInstall(ctx, log, clusterDeployment, imageSetRef)
			if err != nil {
				log.WithError(err).Error("failed to create AgentClusterInstall")
				return ctrl.Result{Requeue: true}, err
			}
		} else {
			log.WithError(err).Error("Failed to get AgentClusterInstall")
			return ctrl.Result{Requeue: true}, err
		}
	}
	clusterDeployment.Spec.ClusterInstallRef = &hivev1.ClusterInstallLocalReference{
		Kind:    "AgentClusterInstall",
		Group:   hiveext.Group,
		Version: hiveext.Version,
		Name:    clusterDeployment.Name,
	}
	r.Update(ctx, clusterDeployment)
	return ctrl.Result{}, nil

}

func (r *AgentClusterReconciler) createAgentClusterInstall(ctx context.Context, log logrus.FieldLogger, clusterDeployment *hivev1.ClusterDeployment, imageSetRef *hivev1.ClusterImageSetReference) error {
	log.Infof("Creating AgentClusterInstall for clusterDeployment: %s %s", clusterDeployment.Namespace, clusterDeployment.Name)
	agentClusterInstall := &hiveext.AgentClusterInstall{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterDeployment.Name,
			Namespace: clusterDeployment.Namespace,
		},
		Spec: hiveext.AgentClusterInstallSpec{
			ImageSetRef: *imageSetRef,
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&capiproviderv1alpha1.AgentCluster{}).
		Complete(r)
}
