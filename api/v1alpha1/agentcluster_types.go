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

package v1alpha1

import (
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1alpha4 "sigs.k8s.io/cluster-api/api/v1alpha4"
)

// AgentClusterSpec defines the desired state of AgentCluster
type AgentClusterSpec struct {
	// ImageSetRef is a reference to a ClusterImageSet. If a value is specified for ReleaseImage,
	// that will take precedence over the one from the ClusterImageSet.
	ImageSetRef *hivev1.ClusterImageSetReference `json:"imageSetRef,omitempty"`

	// ClusterName is the friendly name of the cluster. It is used for subdomains,
	// some resource tagging, and other instances where a friendly name for the
	// cluster is useful.
	// +required
	ClusterName string `json:"clusterName"`

	// BaseDomain is the base domain to which the cluster should belong.
	// +required
	BaseDomain string `json:"baseDomain"`

	// PullSecretRef is the reference to the secret to use when pulling images.
	PullSecretRef *corev1.LocalObjectReference `json:"pullSecretRef,omitempty"`
}

// AgentClusterStatus defines the observed state of AgentCluster
type AgentClusterStatus struct {
	// +optional
	Ready bool `json:"ready"`

	// Conditions defines current service state of the ClusterDeployment.
	// +optional
	Conditions clusterv1alpha4.Conditions `json:"conditions,omitempty"`

	// FailureDomains is a list of failure domain objects synced from the infrastructure provider.
	FailureDomains clusterv1alpha4.FailureDomains `json:"failureDomains,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// AgentCluster is the Schema for the agentclusters API
type AgentCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentClusterSpec   `json:"spec,omitempty"`
	Status AgentClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// AgentClusterList contains a list of AgentCluster
type AgentClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentCluster{}, &AgentClusterList{})
}
