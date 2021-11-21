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
	"encoding/json"
	"errors"
	"strings"
	"time"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_1/types"
	"github.com/go-openapi/swag"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clustererrors "sigs.k8s.io/cluster-api/errors"
	clusterutil "sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
//+kubebuilder:rbac:groups=agent-install.openshift.io,resources=agents,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterdeployments,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch

func (r *AgentMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithFields(
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
		res, err := r.findAgent(ctx, log, agentMachine)
		if err != nil {
			return res, err
		}
		if res.Requeue {
			return res, nil
		}

		// Get AgentMachine with updated status
		if err := r.Get(ctx, req.NamespacedName, agentMachine); err != nil {
			log.WithError(err).Errorf("Failed to get agentMachine %s", req.NamespacedName)
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	agent := &aiv1beta1.Agent{}
	agentRef := types.NamespacedName{Name: agentMachine.Status.AgentRef.Name, Namespace: agentMachine.Status.AgentRef.Namespace}
	if err := r.Get(ctx, agentRef, agent); err != nil {
		log.WithError(err).Errorf("Failed to get agent %s", agentRef)
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, err
	}

	ignitionSet, err := r.setAgentIgnitionEndpoint(ctx, log, agent, agentMachine)
	if err != nil {
		log.WithError(err).Error("failed getting information on ignition endpoint")
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, err
	}
	if !ignitionSet {
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, nil
	}

	// If the AgentMachine has an Agent but the Agent doesn't reference the ClusterDeployment,
	// then set it. At this point we might find that the Agent is already bound and we'll need
	// to find a new one.
	if agent.Spec.ClusterDeploymentName == nil {
		return r.setAgentClusterDeploymentRef(ctx, log, agentMachine, agent)
	}

	// If the AgentMachine has an agent, check its conditions and update ready/error
	return r.updateAgentStatus(ctx, log, agentMachine, agent)
}

func (r *AgentMachineReconciler) findAgent(ctx context.Context, log logrus.FieldLogger, agentMachine *capiproviderv1alpha1.AgentMachine) (ctrl.Result, error) {
	agents := &aiv1beta1.AgentList{}
	if err := r.List(ctx, agents); err != nil {
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, err
	}

	agentMachines := &capiproviderv1alpha1.AgentMachineList{}
	if err := r.List(ctx, agentMachines); err != nil {
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, err
	}
	var foundAgent *aiv1beta1.Agent

	// Find an agent that is unbound and whose validations pass
	for i := 0; i < len(agents.Items) && foundAgent == nil; i++ {
		if isValidAgent(&agents.Items[i], agentMachines) {
			foundAgent = &agents.Items[i]
		}
	}

	if foundAgent == nil {
		log.Info("Did not find any available Agents")
		return ctrl.Result{Requeue: true}, nil
	}

	log.Infof("Found agent to associate with AgentMachine: %s/%s", foundAgent.Namespace, foundAgent.Name)

	agentMachine.Status.AgentRef = &capiproviderv1alpha1.AgentReference{Namespace: foundAgent.Namespace, Name: foundAgent.Name}
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
	agentMachine.Status.Ready = false

	if err := r.Status().Update(ctx, agentMachine); err != nil {
		log.WithError(err).Error("failed to update AgentMachine Status")
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, nil
	}

	return ctrl.Result{}, nil
}

func (r *AgentMachineReconciler) setAgentIgnitionEndpoint(ctx context.Context, log logrus.FieldLogger, agent *aiv1beta1.Agent, agentMachine *capiproviderv1alpha1.AgentMachine) (bool, error) {
	log.Info("Setting Ignition endpoint info")
	machine, err := clusterutil.GetOwnerMachine(ctx, r.Client, agentMachine.ObjectMeta)
	if err != nil {
		return false, err
	}
	if machine == nil {
		log.Info("Waiting for Machine Controller to set OwnerRef on AgentMachine")
		return false, nil
	}

	if machine.Spec.Bootstrap.DataSecretName == nil {
		log.Info("No DataSecretName set, not setting ignition endpoint token")
		return true, nil
	}

	secret := &corev1.Secret{}
	secretRef := types.NamespacedName{Namespace: machine.Namespace, Name: *machine.Spec.Bootstrap.DataSecretName}
	if err := r.Get(ctx, secretRef, secret); err != nil {
		log.WithError(err).Errorf("Failed to get user-data secret %s", *machine.Spec.Bootstrap.DataSecretName)
		return false, err
	}

	ignitionConfig := &ignitionapi.Config{}
	if err := json.Unmarshal(secret.Data["value"], ignitionConfig); err != nil {
		log.WithError(err).Errorf("Failed to unmarshal user-data secret %s", *machine.Spec.Bootstrap.DataSecretName)
		return false, err
	}

	if len(ignitionConfig.Ignition.Config.Merge) != 1 {
		log.Errorf("expected one ignition source in secret %s but found %d", *machine.Spec.Bootstrap.DataSecretName, len(ignitionConfig.Ignition.Config.Merge))
		return false, errors.New("did not find one ignition source as expected")
	}

	ignitionSource := ignitionConfig.Ignition.Config.Merge[0]
	agent.Spec.MachineConfigPool = (*ignitionSource.Source)[strings.LastIndex((*ignitionSource.Source), "/")+1:]
	log.Infof("Setting MachineConfigPool to %s", agent.Spec.MachineConfigPool)

	for _, header := range ignitionSource.HTTPHeaders {
		if header.Name != "Authorization" {
			continue
		}
		expectedPrefix := "Bearer "
		if !strings.HasPrefix(*header.Value, expectedPrefix) {
			log.Errorf("did not find expected prefix for bearer token in user-data secret %s", *machine.Spec.Bootstrap.DataSecretName)
			return false, errors.New("did not find expected prefix for bearer token")
		}
		log.Info("Found Ignition endpoint token")
		agent.Spec.IgnitionEndpointToken = (*header.Value)[len(expectedPrefix):]
	}

	if agentUpdateErr := r.Update(ctx, agent); err != nil {
		log.WithError(agentUpdateErr).Error("failed to update Agent with ClusterDeployment ref")
		return false, err
	}
	log.Info("Successfully updated Ignition endpoint token and MachineConfigPool")
	return true, nil
}

func isValidAgent(agent *aiv1beta1.Agent, agentMachines *capiproviderv1alpha1.AgentMachineList) bool {
	if !agent.Spec.Approved {
		return false
	}
	for _, condition := range agent.Status.Conditions {
		if condition.Type == aiv1beta1.BoundCondition && condition.Status != "False" {
			return false
		}
		if condition.Type == aiv1beta1.ValidatedCondition && condition.Status != "True" {
			return false
		}
	}

	// Make sure no other AgentMachine took this Agent already
	for _, agentMachinePtr := range agentMachines.Items {
		if agentMachinePtr.Status.AgentRef != nil && agentMachinePtr.Status.AgentRef.Namespace == agent.Namespace && agentMachinePtr.Status.AgentRef.Name == agent.Name {
			return false
		}
	}
	return true
}

func (r *AgentMachineReconciler) setAgentClusterDeploymentRef(ctx context.Context, log logrus.FieldLogger, agentMachine *capiproviderv1alpha1.AgentMachine, agent *aiv1beta1.Agent) (ctrl.Result, error) {
	clusterDeploymentRef, err := r.getClusterDeploymentFromAgentMachine(ctx, log, agentMachine)
	if err != nil {
		log.WithError(err).Error("Failed to find ClusterDeploymentRef")
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, err
	}
	if clusterDeploymentRef == nil {
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, nil
	}

	agent.Spec.ClusterDeploymentName = &aiv1beta1.ClusterReference{Namespace: clusterDeploymentRef.Namespace, Name: clusterDeploymentRef.Name}

	if agentUpdateErr := r.Update(ctx, agent); err != nil {
		log.WithError(agentUpdateErr).Error("failed to update Agent with ClusterDeployment ref")
		return ctrl.Result{RequeueAfter: defaultRequeueAfterOnError}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

func (r *AgentMachineReconciler) getClusterDeploymentFromAgentMachine(ctx context.Context, log logrus.FieldLogger, agentMachine *capiproviderv1alpha1.AgentMachine) (*capiproviderv1alpha1.ClusterDeploymentReference, error) {
	// AgentMachine -> CAPI Machine -> Cluster -> ClusterDeployment
	machine, err := clusterutil.GetOwnerMachine(ctx, r.Client, agentMachine.ObjectMeta)
	if err != nil {
		return nil, err
	}
	if machine == nil {
		log.Info("Waiting for Machine Controller to set OwnerRef on AgentMachine")
		return nil, nil
	}

	cluster, err := clusterutil.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		log.Info("Machine is missing cluster label or cluster does not exist")
		return nil, nil
	}

	agentClusterRef := types.NamespacedName{Name: cluster.Spec.InfrastructureRef.Name, Namespace: cluster.Spec.InfrastructureRef.Namespace}
	agentCluster := &capiproviderv1alpha1.AgentCluster{}
	if err := r.Get(ctx, agentClusterRef, agentCluster); err != nil {
		log.WithError(err).Errorf("Failed to get agentCluster %s", agentClusterRef)
		return nil, err
	}

	return &agentCluster.Status.ClusterDeploymentRef, nil
}

func (r *AgentMachineReconciler) updateAgentStatus(ctx context.Context, log logrus.FieldLogger, agentMachine *capiproviderv1alpha1.AgentMachine, agent *aiv1beta1.Agent) (ctrl.Result, error) {
	log.Info("Updating agentMachine status")
	for _, condition := range agent.Status.Conditions {
		if condition.Type == aiv1beta1.InstalledCondition {
			if condition.Status == "True" {
				log.Info("Updating agentMachine status to Ready=true")
				agentMachine.Status.Ready = true
			} else if condition.Status == "False" {
				if condition.Reason == aiv1beta1.InstallationFailedReason {
					agentMachine.Status.FailureReason = (*clustererrors.MachineStatusError)(&condition.Reason)
					agentMachine.Status.FailureMessage = &condition.Message
				}
			}
			break
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
