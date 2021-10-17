package controllers

import (
	"context"
	capiproviderv1alpha1 "github.com/eranco74/cluster-api-provider-agent/api/v1alpha1"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// move to common
func GetTestLog() logrus.FieldLogger {
	l := logrus.New()
	return l
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
			Log:    GetTestLog(),
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("none existing agentCluster", func() {
		agentCluster := newAgentCluster("agentCluster-1", testNamespace, capiproviderv1alpha1.AgentClusterSpec{})
		Expect(c.Create(ctx, agentCluster)).To(BeNil())

		noneExistingHost := newAgentCluster("agentCluster-2", testNamespace, capiproviderv1alpha1.AgentClusterSpec{})

		result, err := acr.Reconcile(ctx, newAgentClusterRequest(noneExistingHost))
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

	// currently fail due to: "no kind is registered for the type v1.ClusterDeployment in scheme"
	//It("create clusterDeployment for agentCluster", func() {
	//	domain := "test-domain.com"
	//	clusterName := "test-cluster-name"
	//	pullSecretName := "test-pull-secret-name"
	//	agentCluster := newAgentCluster("agentCluster-1", testNamespace, capiproviderv1alpha1.AgentClusterSpec{
	//		BaseDomain:  domain,
	//		ClusterName: clusterName,
	//		PullSecretRef: &corev1.LocalObjectReference{
	//			Name: pullSecretName,
	//		},
	//	})
	//	Expect(c.Create(ctx, agentCluster)).To(BeNil())
	//
	//	result, err := acr.Reconcile(ctx, newAgentClusterRequest(agentCluster))
	//	Expect(err).To(BeNil())
	//	Expect(result).To(Equal(ctrl.Result{}))
	//
	//	key := types.NamespacedName{
	//		Namespace: testNamespace,
	//		Name:      "agentCluster-1",
	//	}
	//	logrus.Infof("%v", hivev1.ClusterDeployment{})
	//	Expect(c.Get(ctx, key, agentCluster)).To(BeNil())
	//	Expect(agentCluster.Status.ClusterDeploymentRef.Name).ToNot(Equal(""))
	//})

})
