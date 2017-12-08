package routes

import (
	"fmt"
	"time"

	g "github.com/onsi/ginkgo"
	o "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	routeutil "github.com/tnozicka/openshift-acme/pkg/route"
	"github.com/tnozicka/openshift-acme/test/e2e/framework"
	"github.com/tnozicka/openshift-acme/test/e2e/openshift/util"
)

const (
	RouteAdmissionTimeout          = 5 * time.Second
	CertificateProvisioningTimeout = 60 * time.Second
)

var _ = g.Describe("Routes", func() {
	defer g.GinkgoRecover()
	f := framework.NewFramework("routes")

	g.It("should create cert for annotated Route", func() {
		namespace := f.Namespace()

		route := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
				Annotations: map[string]string{
					"kubernetes.io/tls-acme": "true",
				},
			},
			Spec: routev1.RouteSpec{
				Host: util.Domain(),
				To: routev1.RouteTargetReference{
					Name: "non-existing",
				},
			},
		}

		route, err := f.RouteClientset().RouteV1().Routes(namespace).Create(route)
		o.Expect(err).NotTo(o.HaveOccurred())

		w, err := f.RouteClientset().RouteV1().Routes(namespace).Watch(metav1.SingleObject(route.ObjectMeta))
		o.Expect(err).NotTo(o.HaveOccurred())
		event, err := watch.Until(RouteAdmissionTimeout, w, func(event watch.Event) (bool, error) {
			switch event.Type {
			case watch.Modified:
				r := event.Object.(*routev1.Route)
				if routeutil.IsAdmitted(r) {
					return true, nil
				}

				return false, nil
			default:
				return false, fmt.Errorf("unexpected event - type: %s, obj: %#v", event.Type, event.Object)
			}
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to wait for Route to be admitted by the router!")

		w, err = f.RouteClientset().RouteV1().Routes(namespace).Watch(metav1.SingleObject(route.ObjectMeta))
		o.Expect(err).NotTo(o.HaveOccurred())
		event, err = watch.Until(CertificateProvisioningTimeout, w, func(event watch.Event) (bool, error) {
			switch event.Type {
			case watch.Modified:
				r := event.Object.(*routev1.Route)
				if r.Spec.TLS != nil {
					return true, nil
				}

				return false, nil
			default:
				return false, fmt.Errorf("unexpected event - type: %s, obj: %#v", event.Type, event.Object)
			}
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to wait for certificate to be provisioned!")

		route = event.Object.(*routev1.Route)
		o.Expect(route.Spec.TLS).NotTo(o.BeNil())

		o.Expect(route.Spec.TLS.Termination).To(o.Equal(routev1.TLSTerminationEdge))

		crt, err := util.CertificateFromPEM([]byte(route.Spec.TLS.Certificate))
		o.Expect(err).NotTo(o.HaveOccurred())

		now := time.Now()
		o.Expect(now.Before(crt.NotBefore)).To(o.BeFalse())
		o.Expect(now.After(crt.NotAfter)).To(o.BeFalse())
	})
})
