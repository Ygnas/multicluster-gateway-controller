//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"time"

	certmanv1 "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"github.com/Kuadrant/multicluster-gateway-controller/pkg/_internal/conditions"
	"github.com/Kuadrant/multicluster-gateway-controller/pkg/apis/v1alpha1"
	. "github.com/Kuadrant/multicluster-gateway-controller/pkg/controllers/tlspolicy"
	. "github.com/Kuadrant/multicluster-gateway-controller/test/util"
)

var _ = Describe("TLSPolicy", Ordered, func() {

	var testNamespace string
	var gatewayClass *gatewayv1beta1.GatewayClass
	var issuer *certmanv1.Issuer

	BeforeAll(func() {
		logger = zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true))
		logger.WithName("tlspolicy_controller_test")
		logf.SetLogger(logger)

		gatewayClass = testBuildGatewayClass("kuadrant-multi-cluster-gateway-instance-per-cluster", "default")
		Expect(k8sClient.Create(ctx, gatewayClass)).To(BeNil())
		Eventually(func() error { // gateway class exists
			return k8sClient.Get(ctx, client.ObjectKey{Name: gatewayClass.Name}, gatewayClass)
		}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
	})

	BeforeEach(func() {
		CreateNamespace(&testNamespace)
		issuer = NewTestIssuer("testissuer", testNamespace)
		Expect(k8sClient.Create(ctx, issuer)).To(BeNil())
		Eventually(func() error { //issuer exists
			return k8sClient.Get(ctx, client.ObjectKey{Name: issuer.Name, Namespace: issuer.Namespace}, issuer)
		}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		gatewayList := &gatewayv1beta1.GatewayList{}
		Expect(k8sClient.List(ctx, gatewayList)).To(BeNil())
		for _, gw := range gatewayList.Items {
			k8sClient.Delete(ctx, &gw)
		}
		policyList := v1alpha1.TLSPolicyList{}
		Expect(k8sClient.List(ctx, &policyList)).To(BeNil())
		for _, policy := range policyList.Items {
			k8sClient.Delete(ctx, &policy)
		}
		issuerList := certmanv1.IssuerList{}
		Expect(k8sClient.List(ctx, &issuerList)).To(BeNil())
		for _, issuer := range issuerList.Items {
			k8sClient.Delete(ctx, &issuer)
		}
	})

	AfterAll(func() {
		err := k8sClient.Delete(ctx, gatewayClass)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("istio gateway", func() {
		var gateway *gatewayv1beta1.Gateway
		var tlsPolicy *v1alpha1.TLSPolicy
		gwClassName := "istio"

		AfterEach(func() {
			err := k8sClient.Delete(ctx, gateway)
			Expect(err).ToNot(HaveOccurred())
			err = k8sClient.Delete(ctx, tlsPolicy)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("valid target, issuer and policy", func() {

			BeforeEach(func() {
				gateway = NewTestGateway("test-gateway", gwClassName, testNamespace).
					WithHTTPListener("test.example.com").Gateway
				Expect(k8sClient.Create(ctx, gateway)).To(BeNil())
				Eventually(func() error { //gateway exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: gateway.Name, Namespace: gateway.Namespace}, gateway)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
				tlsPolicy = NewTestTLSPolicy("test-tls-policy", testNamespace).
					WithTargetGateway(gateway.Name).
					WithIssuer("testissuer", certmanv1.IssuerKind, "cert-manager.io").TLSPolicy
				Expect(k8sClient.Create(ctx, tlsPolicy)).To(BeNil())
				Eventually(func() error { //tls policy exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: tlsPolicy.Name, Namespace: tlsPolicy.Namespace}, tlsPolicy)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
			})

			It("should have ready status", func() {
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: tlsPolicy.Name, Namespace: tlsPolicy.Namespace}, tlsPolicy); err != nil {
						return err
					}
					if !meta.IsStatusConditionTrue(tlsPolicy.Status.Conditions, string(conditions.ConditionTypeReady)) {
						return fmt.Errorf("expected tlsPolicy status condition to be %s", string(conditions.ConditionTypeReady))
					}
					return nil
				}, time.Second*15, time.Second).Should(BeNil())
			})

			It("should set gateway back reference", func() {
				existingGateway := &gatewayv1beta1.Gateway{}
				policyBackRefValue := testNamespace + "/" + tlsPolicy.Name
				refs, _ := json.Marshal([]client.ObjectKey{{Name: tlsPolicy.Name, Namespace: testNamespace}})
				policiesBackRefValue := string(refs)
				Eventually(func() error {
					// Check gateway back references
					err := k8sClient.Get(ctx, client.ObjectKey{Name: gateway.Name, Namespace: testNamespace}, existingGateway)
					Expect(err).ToNot(HaveOccurred())
					annotations := existingGateway.GetAnnotations()
					if annotations == nil {
						return fmt.Errorf("existingGateway annotations should not be nil")
					}
					if _, ok := annotations[TLSPolicyBackRefAnnotation]; !ok {
						return fmt.Errorf("existingGateway annotations do not have annotation %s", TLSPolicyBackRefAnnotation)
					}
					if annotations[TLSPolicyBackRefAnnotation] != policyBackRefValue {
						return fmt.Errorf("existingGateway annotations[%s] does not have expected value", TLSPolicyBackRefAnnotation)
					}
					return nil
				}, time.Second*5, time.Second).Should(BeNil())
				Eventually(func() error {
					// Check gateway back references
					err := k8sClient.Get(ctx, client.ObjectKey{Name: gateway.Name, Namespace: testNamespace}, existingGateway)
					Expect(err).ToNot(HaveOccurred())
					annotations := existingGateway.GetAnnotations()
					if annotations == nil {
						return fmt.Errorf("existingGateway annotations should not be nil")
					}
					if _, ok := annotations[TLSPoliciesBackRefAnnotation]; !ok {
						return fmt.Errorf("existingGateway annotations do not have annotation %s", TLSPoliciesBackRefAnnotation)
					}
					if annotations[TLSPoliciesBackRefAnnotation] != policiesBackRefValue {
						return fmt.Errorf("existingGateway annotations[%s] does not have expected value", TLSPoliciesBackRefAnnotation)
					}
					return nil
				}, time.Second*5, time.Second).Should(BeNil())
			})

			It("should set policy affected condition in gateway status", func() {
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(gateway), gateway); err != nil {
						return err
					}

					policyAffectedCond := meta.FindStatusCondition(gateway.Status.Conditions, string(TLSPolicyAffected))
					if policyAffectedCond == nil {
						return fmt.Errorf("policy affected conditon expected but not found")
					}
					if policyAffectedCond.ObservedGeneration != gateway.Generation {
						return fmt.Errorf("expected policy affected cond generation to be %d but got %d", gateway.Generation, policyAffectedCond.ObservedGeneration)
					}
					if !meta.IsStatusConditionTrue(gateway.Status.Conditions, string(TLSPolicyAffected)) {
						return fmt.Errorf("expected gateway status condition %s to be True", TLSPolicyAffected)
					}

					return nil
				}, time.Second*15, time.Second).Should(BeNil())
			})

		})

		Context("valid target, clusterissuer and policy", func() {
			var clusterIssuer *certmanv1.ClusterIssuer

			BeforeEach(func() {
				gateway = NewTestGateway("test-gateway", gwClassName, testNamespace).
					WithHTTPListener("test.example.com").Gateway
				Expect(k8sClient.Create(ctx, gateway)).To(BeNil())
				Eventually(func() error { //gateway exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: gateway.Name, Namespace: gateway.Namespace}, gateway)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
				tlsPolicy = NewTestTLSPolicy("test-tls-policy", testNamespace).
					WithTargetGateway(gateway.Name).
					WithIssuer("testclusterissuer", certmanv1.ClusterIssuerKind, "cert-manager.io").TLSPolicy
				Expect(k8sClient.Create(ctx, tlsPolicy)).To(BeNil())
				Eventually(func() error { //tls policy exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: tlsPolicy.Name, Namespace: tlsPolicy.Namespace}, tlsPolicy)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
				clusterIssuer = NewTestClusterIssuer("testclusterissuer")
				Expect(k8sClient.Create(ctx, clusterIssuer)).To(BeNil())
				Eventually(func() error { //clusterIssuer exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: clusterIssuer.Name}, clusterIssuer)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
			})

			It("should have ready status", func() {
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKey{Name: tlsPolicy.Name, Namespace: tlsPolicy.Namespace}, tlsPolicy); err != nil {
						return err
					}
					if !meta.IsStatusConditionTrue(tlsPolicy.Status.Conditions, string(conditions.ConditionTypeReady)) {
						return fmt.Errorf("expected tlsPolicy status condition to be %s", string(conditions.ConditionTypeReady))
					}
					return nil
				}, time.Second*15, time.Second).Should(BeNil())
			})
		})

		Context("with http listener", func() {

			BeforeEach(func() {
				gateway = NewTestGateway("test-gateway", gwClassName, testNamespace).
					WithHTTPListener("test.example.com").Gateway
				Expect(k8sClient.Create(ctx, gateway)).To(BeNil())
				Eventually(func() error { //gateway exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: gateway.Name, Namespace: gateway.Namespace}, gateway)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
				tlsPolicy = NewTestTLSPolicy("test-tls-policy", testNamespace).
					WithTargetGateway(gateway.Name).
					WithIssuer("testissuer", certmanv1.IssuerKind, "cert-manager.io").TLSPolicy
				Expect(k8sClient.Create(ctx, tlsPolicy)).To(BeNil())
				Eventually(func() error { //tls policy exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: tlsPolicy.Name, Namespace: tlsPolicy.Namespace}, tlsPolicy)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
			})

			It("should not create any certificates when TLS is not present", func() {
				Consistently(func() []certmanv1.Certificate {
					certList := &certmanv1.CertificateList{}
					err := k8sClient.List(ctx, certList, &client.ListOptions{Namespace: testNamespace})
					Expect(err).ToNot(HaveOccurred())
					return certList.Items
				}, time.Second*10, time.Second).Should(BeEmpty())
			})

			It("should create certificate when TLS is present", func() {
				certNS := gatewayv1beta1.Namespace(testNamespace)
				patch := client.MergeFrom(gateway.DeepCopy())
				gateway.Spec.Listeners[0].TLS = &gatewayv1beta1.GatewayTLSConfig{
					Mode: Pointer(gatewayv1beta1.TLSModeTerminate),
					CertificateRefs: []gatewayv1beta1.SecretObjectReference{
						{
							Name:      "test-tls-secret",
							Namespace: &certNS,
						},
					},
				}
				Expect(k8sClient.Patch(ctx, gateway, patch)).To(BeNil())
				Eventually(func() error {
					cert := &certmanv1.Certificate{}
					return k8sClient.Get(ctx, client.ObjectKey{Name: "test-tls-secret", Namespace: testNamespace}, cert)
				}, time.Second*10, time.Second).Should(BeNil())
			})

		})

		Context("with https listener", func() {

			BeforeEach(func() {
				gateway = NewTestGateway("test-gateway", gwClassName, testNamespace).
					WithHTTPSListener("test.example.com", "test-tls-secret").Gateway
				Expect(k8sClient.Create(ctx, gateway)).To(BeNil())
				Eventually(func() error { //gateway exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: gateway.Name, Namespace: gateway.Namespace}, gateway)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
				tlsPolicy = NewTestTLSPolicy("test-tls-policy", testNamespace).
					WithTargetGateway(gateway.Name).
					WithIssuer("testissuer", certmanv1.IssuerKind, "cert-manager.io").TLSPolicy
				Expect(k8sClient.Create(ctx, tlsPolicy)).To(BeNil())
				Eventually(func() error { //tls policy exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: tlsPolicy.Name, Namespace: tlsPolicy.Namespace}, tlsPolicy)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
			})

			It("should create tls certificate", func() {
				Eventually(func() error {
					certList := &certmanv1.CertificateList{}
					err := k8sClient.List(ctx, certList, &client.ListOptions{Namespace: testNamespace})
					Expect(err).ToNot(HaveOccurred())
					if len(certList.Items) != 1 {
						return fmt.Errorf("expected certificate List to be 1")
					}
					return nil
				}, time.Second*10, time.Second).Should(BeNil())

				cert1 := &certmanv1.Certificate{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: "test-tls-secret", Namespace: testNamespace}, cert1)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("with multiple https listener", func() {

			BeforeEach(func() {
				gateway = NewTestGateway("test-gateway", gwClassName, testNamespace).
					WithHTTPSListener("test1.example.com", "test-tls-secret").
					WithHTTPSListener("test2.example.com", "test-tls-secret").
					WithHTTPSListener("test3.example.com", "test2-tls-secret").Gateway
				Expect(k8sClient.Create(ctx, gateway)).To(BeNil())
				Eventually(func() error { //gateway exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: gateway.Name, Namespace: gateway.Namespace}, gateway)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
				tlsPolicy = NewTestTLSPolicy("test-tls-policy", testNamespace).
					WithTargetGateway(gateway.Name).
					WithIssuer("testissuer", certmanv1.IssuerKind, "cert-manager.io").TLSPolicy
				Expect(k8sClient.Create(ctx, tlsPolicy)).To(BeNil())
				Eventually(func() error { //tls policy exists
					return k8sClient.Get(ctx, client.ObjectKey{Name: tlsPolicy.Name, Namespace: tlsPolicy.Namespace}, tlsPolicy)
				}, TestTimeoutMedium, TestRetryIntervalMedium).ShouldNot(HaveOccurred())
			})

			It("should create tls certificates", func() {
				Eventually(func() error {
					certList := &certmanv1.CertificateList{}
					err := k8sClient.List(ctx, certList, &client.ListOptions{Namespace: testNamespace})
					Expect(err).ToNot(HaveOccurred())
					if len(certList.Items) != 2 {
						return fmt.Errorf("expected CertificateList to be 2")
					}
					return nil
				}, time.Second*10, time.Second).Should(BeNil())

				cert1 := &certmanv1.Certificate{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: "test-tls-secret", Namespace: testNamespace}, cert1)
				Expect(err).ToNot(HaveOccurred())

				cert2 := &certmanv1.Certificate{}
				err = k8sClient.Get(ctx, client.ObjectKey{Name: "test2-tls-secret", Namespace: testNamespace}, cert2)
				Expect(err).ToNot(HaveOccurred())
			})
		})

	})

})
