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
	"time"

	"github.com/go-openapi/swag"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	logutil "github.com/openshift/assisted-service/pkg/log"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/errors"
	clusterutil "sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	capiproviderv1alpha1 "github.com/eranco74/cluster-api-provider-agent/api/v1alpha1"
)

const defaultRequeueAfterOnError = 10 * time.Second

// AgentMachineReconciler reconciles a AgentMachine object
type AgentMachineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logrus.FieldLogger
}

//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentmachines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentmachines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentmachines/finalizers,verbs=update

func (r *AgentMachineReconciler) Reconcile(originalCtx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx := addRequestIdIfNeeded(originalCtx)
	log := logutil.FromContext(ctx, r.Log).WithFields(
		logrus.Fields{
			"agent_machine":           req.Name,
			"agent_machine_namespace": req.Namespace,
		})

	defer func() {
		log.Info("AgentMachine Reconcile ended")
	}()

	agentMachine := &capiproviderv1alpha1.AgentMachine{}
	if err := r.Get(ctx, req.NamespacedName, agentMachine); err != nil {
		log.WithError(err).Errorf("Failed to get agentMachine %s", req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// If the AgentMachine is ready, we have nothing to do
	if agentMachine.Status.Ready {
		return ctrl.Result{}, nil
	}

	// If the AgentMachine doesn't have an agent, find one and set the agentRef
	if agentMachine.Status.AgentRef == nil {
		return r.findAgent(ctx, log, agentMachine)
	}

	// If the AgentMachine has an agent, check its conditions and update ready/error
	return r.updateAgentStatus(ctx, log, agentMachine)
}

func (r *AgentMachineReconciler) findAgent(ctx context.Context, log logrus.FieldLogger, agentMachine *capiproviderv1alpha1.AgentMachine) (ctrl.Result, error) {
	agents := &aiv1beta1.AgentList{}
	if err := r.List(ctx, agents); err != nil {
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, err
	}

	var foundAgent aiv1beta1.Agent

	for _, agent := range agents.Items {
	OUTER:
		// Take the first unbound and validated agent that we find for now
		for _, condition := range agent.Status.Conditions {
			if condition.Type == aiv1beta1.BoundCondition && condition.Status != "False" {
				continue OUTER
			}
			if condition.Type == aiv1beta1.ValidatedCondition && condition.Status != "True" {
				continue OUTER
			}
		}
		foundAgent = agent
		break
	}

	log.Infof("Found agent to associate with AgentMachine: %s/%s", foundAgent.Namespace, foundAgent.Name)
	agentMachine.Status.AgentRef.Name = foundAgent.Name
	agentMachine.Status.AgentRef.Namespace = foundAgent.Namespace
	agentMachine.Spec.ProviderID = swag.String("agent://" + foundAgent.Status.Inventory.SystemVendor.SerialNumber)

	var machineAddresses []clusterv1.MachineAddress
	for _, iface := range foundAgent.Status.Inventory.Interfaces {
		if !iface.HasCarrier {
			continue
		}
		for _, addr := range iface.IPV4Addresses {
			machineAddresses = append(machineAddresses, clusterv1.MachineAddress{
				Type:    clusterv1.MachineExternalIP,
				Address: addr,
			})
		}
	}
	agentMachine.Status.Addresses = machineAddresses

	// We need to set the ClusterRef on the Agent, so we need to get the CAPI Machine,
	// which points to the Cluster, which points to the ClusterDeployment
	machine, err := clusterutil.GetOwnerMachine(ctx, r.Client, agentMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		log.Info("Waiting for Machine Controller to set OwnerRef on AgentMachine")
		return ctrl.Result{}, nil
	}
	cluster, err := clusterutil.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		log.Info("Machine is missing cluster label or cluster does not exist")
		return reconcile.Result{}, nil
	}
	agentClusterRef := cluster.Spec.InfrastructureRef
	// TODO: Get AgentCluster and ref to ClusterDeployment, set ClusterRef on Agent to it
	log.Infof("AgentClusterRef: %s", agentClusterRef.UID)

	if agentUpdateErr := r.Update(ctx, &foundAgent); err != nil {
		log.WithError(agentUpdateErr).Error("failed to update Agent")
		return ctrl.Result{Requeue: true}, nil
	}

	// If we fail to update the AgentMachine, but already updated the Agent, we're inconsistent
	if updateErr := r.Status().Update(ctx, agentMachine); updateErr != nil {
		log.WithError(updateErr).Error("failed to update AgentMachine Status")
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (r *AgentMachineReconciler) updateAgentStatus(ctx context.Context, log logrus.FieldLogger, agentMachine *capiproviderv1alpha1.AgentMachine) (ctrl.Result, error) {
	agent := &aiv1beta1.Agent{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: agentMachine.Status.AgentRef.Namespace, Name: agentMachine.Status.AgentRef.Name}, agent); err != nil {
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, err
	}

	for _, condition := range agent.Status.Conditions {
		if condition.Type == aiv1beta1.InstalledCondition {
			if condition.Status == "True" {
				agentMachine.Status.Ready = true
			} else if condition.Status == "False" {
				if condition.Reason == aiv1beta1.InstallationFailedReason {
					agentMachine.Status.FailureReason = (*errors.MachineStatusError)(&condition.Reason)
					agentMachine.Status.FailureMessage = &condition.Message
				}
			}
		}
	}

	if updateErr := r.Status().Update(ctx, agentMachine); updateErr != nil {
		log.WithError(updateErr).Error("failed to update AgentMachine Status")
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&capiproviderv1alpha1.AgentMachine{}).
		Complete(r)
}
