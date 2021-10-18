package controllers

import (
	"context"
	"fmt"
	capiproviderv1alpha1 "github.com/eranco74/cluster-api-provider-agent/api/v1alpha1"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	hiveext "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func init() {
	_ = hivev1.AddToScheme(scheme.Scheme)
	_ = hiveext.AddToScheme(scheme.Scheme)
	_ = capiproviderv1alpha1.AddToScheme(scheme.Scheme)
}

func newAgentClusterRequest(agentCluster *capiproviderv1alpha1.AgentCluster) ctrl.Request {
	namespacedName := types.NamespacedName{
		Namespace: agentCluster.ObjectMeta.Namespace,
		Name:      agentCluster.ObjectMeta.Name,
	}
	return ctrl.Request{NamespacedName: namespacedName}
}

func newAgentCluster(name, namespace string, spec capiproviderv1alpha1.AgentClusterSpec) *capiproviderv1alpha1.AgentCluster {
	return &capiproviderv1alpha1.AgentCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: spec,
	}
}

var _ = Describe("agentcluster reconcile", func() {
	var (
		c             client.Client
		acr           *AgentClusterReconciler
		ctx           = context.Background()
		mockCtrl      *gomock.Controller
		testNamespace = "test-namespace"
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).Build()
		mockCtrl = gomock.NewController(GinkgoT())

		acr = &AgentClusterReconciler{
			Client: c,
			Scheme: scheme.Scheme,
			Log:    logrus.New(),
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("none existing agentCluster", func() {
		agentCluster := newAgentCluster("agentCluster-1", testNamespace, capiproviderv1alpha1.AgentClusterSpec{})
		Expect(c.Create(ctx, agentCluster)).To(BeNil())

		noneExistingAgentCluster := newAgentCluster("agentCluster-2", testNamespace, capiproviderv1alpha1.AgentClusterSpec{})

		result, err := acr.Reconcile(ctx, newAgentClusterRequest(noneExistingAgentCluster))
		Expect(err).To(BeNil())
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("agentCluster ready status", func() {
		agentCluster := newAgentCluster("agentCluster-1", testNamespace, capiproviderv1alpha1.AgentClusterSpec{})
		agentCluster.Status.Ready = true
		Expect(c.Create(ctx, agentCluster)).To(BeNil())

		result, err := acr.Reconcile(ctx, newAgentClusterRequest(agentCluster))
		Expect(err).To(BeNil())
		Expect(result).To(Equal(ctrl.Result{}))
	})
	It("create clusterDeployment for agentCluster", func() {
		domain := "test-domain.com"
		clusterName := "test-cluster-name"
		pullSecretName := "test-pull-secret-name"
		agentCluster := newAgentCluster("agentCluster-1", testNamespace, capiproviderv1alpha1.AgentClusterSpec{
			BaseDomain:  domain,
			ClusterName: clusterName,
			PullSecretRef: &corev1.LocalObjectReference{
				Name: pullSecretName,
			},
		})
		Expect(c.Create(ctx, agentCluster)).To(BeNil())

		result, err := acr.Reconcile(ctx, newAgentClusterRequest(agentCluster))
		Expect(err).To(BeNil())
		Expect(result).To(Equal(ctrl.Result{}))

		key := types.NamespacedName{
			Namespace: testNamespace,
			Name:      "agentCluster-1",
		}
		Expect(c.Get(ctx, key, agentCluster)).To(BeNil())
		fmt.Printf("%+v", agentCluster)
		Expect(agentCluster.Status.ClusterDeploymentRef.Name).ToNot(Equal(""))
	})
	It("failed to find clusterDeployment", func() {
		domain := "test-domain.com"
		clusterName := "test-cluster-name"
		pullSecretName := "test-pull-secret-name"
		agentCluster := newAgentCluster("agentCluster-1", testNamespace, capiproviderv1alpha1.AgentClusterSpec{
			BaseDomain:  domain,
			ClusterName: clusterName,
			PullSecretRef: &corev1.LocalObjectReference{
				Name: pullSecretName,
			},
		})
		agentCluster.Status.ClusterDeploymentRef.Name = "missing-cluster-deployment-name"
		Expect(c.Create(ctx, agentCluster)).To(BeNil())

		result, err := acr.Reconcile(ctx, newAgentClusterRequest(agentCluster))
		Expect(err).ToNot(BeNil())
		Expect(err.Error()).To(MatchRegexp("not found"))
		Expect(result).To(Equal(ctrl.Result{Requeue: true}))
	})
	It("create AgentClusterInstall for agentCluster", func() {
		domain := "test-domain.com"
		clusterName := "test-cluster-name"
		pullSecretName := "test-pull-secret-name"
		agentCluster := newAgentCluster("agentCluster-1", testNamespace, capiproviderv1alpha1.AgentClusterSpec{
			BaseDomain:  domain,
			ClusterName: clusterName,
			PullSecretRef: &corev1.LocalObjectReference{
				Name: pullSecretName,
			},
			ImageSetRef: &hivev1.ClusterImageSetReference{Name: "test-image-set"},
		})
		agentCluster.Status.ClusterDeploymentRef.Name = agentCluster.Name
		agentCluster.Status.ClusterDeploymentRef.Namespace = agentCluster.Namespace
		Expect(c.Create(ctx, agentCluster)).To(BeNil())

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
		Expect(c.Create(ctx, clusterDeployment)).To(BeNil())

		result, err := acr.Reconcile(ctx, newAgentClusterRequest(agentCluster))
		Expect(err).To(BeNil())
		Expect(result).To(Equal(ctrl.Result{}))

		key := types.NamespacedName{
			Namespace: testNamespace,
			Name:      "agentCluster-1",
		}
		Expect(c.Get(ctx, key, agentCluster)).To(BeNil())
		fmt.Printf("%+v", agentCluster)
		Expect(agentCluster.Status.ClusterDeploymentRef.Name).ToNot(Equal(""))
	})
})
