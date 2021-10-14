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

	logutil "github.com/openshift/assisted-service/pkg/log"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	capiproviderv1alpha1 "github.com/eranco74/cluster-api-provider-agent/api/v1alpha1"
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

	// If the agentCluster has references a ClusterDeployment, sync from its status
	return r.updateClusterStatus(ctx, log, agentCluster)
}

func (r *AgentClusterReconciler) createClusterDeployment(ctx context.Context, log logrus.FieldLogger, agentCluster *capiproviderv1alpha1.AgentCluster) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *AgentClusterReconciler) updateClusterStatus(ctx context.Context, log logrus.FieldLogger, agentCluster *capiproviderv1alpha1.AgentCluster) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&capiproviderv1alpha1.AgentCluster{}).
		Complete(r)
}
