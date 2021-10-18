package controllers

import (
	"context"
	capiproviderv1alpha1 "github.com/eranco74/cluster-api-provider-agent/api/v1alpha1"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	hiveext "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/sirupsen/logrus"
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

func newAgentMachineRequest(agentMachine *capiproviderv1alpha1.AgentMachine) ctrl.Request {
	namespacedName := types.NamespacedName{
		Namespace: agentMachine.ObjectMeta.Namespace,
		Name:      agentMachine.ObjectMeta.Name,
	}
	return ctrl.Request{NamespacedName: namespacedName}
}

func newAgentMachine(name, namespace string, spec capiproviderv1alpha1.AgentMachineSpec) *capiproviderv1alpha1.AgentMachine {
	return &capiproviderv1alpha1.AgentMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: spec,
	}
}

var _ = Describe("agentmachine reconcile", func() {
	var (
		c             client.Client
		amr           *AgentMachineReconciler
		ctx           = context.Background()
		mockCtrl      *gomock.Controller
		testNamespace = "test-namespace"
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).Build()
		mockCtrl = gomock.NewController(GinkgoT())

		amr = &AgentMachineReconciler{
			Client: c,
			Scheme: scheme.Scheme,
			Log:    logrus.New(),
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("none existing agentMachine", func() {
		agentMachine := newAgentMachine("agentMachine-1", testNamespace, capiproviderv1alpha1.AgentMachineSpec{})
		Expect(c.Create(ctx, agentMachine)).To(BeNil())

		noneExistingAgentMachine := newAgentMachine("agentMachine-2", testNamespace, capiproviderv1alpha1.AgentMachineSpec{})

		result, err := amr.Reconcile(ctx, newAgentMachineRequest(noneExistingAgentMachine))
		Expect(err).To(BeNil())
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("agentMachine ready status", func() {
		agentMachine := newAgentMachine("agentMachine-1", testNamespace, capiproviderv1alpha1.AgentMachineSpec{})
		agentMachine.Status.Ready = true
		Expect(c.Create(ctx, agentMachine)).To(BeNil())

		result, err := amr.Reconcile(ctx, newAgentMachineRequest(agentMachine))
		Expect(err).To(BeNil())
		Expect(result).To(Equal(ctrl.Result{}))
	})
})
