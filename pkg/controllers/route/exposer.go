package route

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang/glog"
	routev1 "github.com/openshift/api/route/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned"
	routeutil "github.com/tnozicka/openshift-acme/pkg/route"
	"github.com/tnozicka/openshift-acme/pkg/util"
	"golang.org/x/crypto/acme"
	corev1 "k8s.io/api/core/v1"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"

	"github.com/tnozicka/openshift-acme/pkg/acme/challengeexposers"
	"github.com/tnozicka/openshift-acme/pkg/api"
)

const (
	RouterAdmitTimeout = 30 * time.Second
)

type Exposer struct {
	underlyingExposer challengeexposers.Interface
	routeClientset    routeclientset.Interface
	kubeClientset     kubernetes.Interface
	recorder          record.EventRecorder
	exposerIP         string
	exposerPort       int32
	route             *routev1.Route
}

var _ challengeexposers.Interface = &Exposer{}

func NewExposer(underlyingExposer challengeexposers.Interface,
	routeClientset routeclientset.Interface,
	kubeClientset kubernetes.Interface,
	recorder record.EventRecorder,
	exposerIP string,
	exposerPort int32,
	route *routev1.Route,
) *Exposer {
	return &Exposer{
		underlyingExposer: underlyingExposer,
		routeClientset:    routeClientset,
		kubeClientset:     kubeClientset,
		recorder:          recorder,
		exposerIP:         exposerIP,
		exposerPort:       exposerPort,
		route:             route,
	}
}

func (e *Exposer) exposingTmpName() string {
	return fmt.Sprintf("%s-%s", e.route.Name, api.ForwardingRouteSuffing)
}

func (e *Exposer) Expose(c *acme.Client, domain string, token string) error {
	// Name of the forwarding Service and Route
	exposingName := e.exposingTmpName()
	trueVal := true

	// Route can only point to a Service in the same namespace
	// but we need to redirect ACME challenge to this controller
	// usually deployed in a different namespace.
	// We avoid this limitation by creating a forwarding service and manual endpoints.

	/*
	   Service
	*/
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: exposingName,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         routev1.SchemeGroupVersion.String(),
					Kind:               "Route",
					Name:               e.route.Name,
					UID:                e.route.UID,
					Controller:         &trueVal,
					BlockOwnerDeletion: &trueVal,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
		},
	}
	createdService, err := e.kubeClientset.CoreV1().Services(e.route.Namespace).Create(service)
	if err != nil {
		if !kapierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create exposing Service %s/%s: %v", service.Namespace, service.Name, err)
		}

		glog.Warningf("Forwarding Service %s/%s already exists, forcing rewrite", createdService.Namespace, createdService.Name)

		preexistingService, err := e.kubeClientset.CoreV1().Services(e.route.Namespace).Get(service.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get exposing Service %s/%s before updating: %v", service.Namespace, service.Name, err)
		}

		service.ResourceVersion = preexistingService.ResourceVersion
		createdService, err = e.kubeClientset.CoreV1().Services(e.route.Namespace).Update(service)
		if err != nil {
			return fmt.Errorf("failed to update exposing Service %s/%s: %v", service.Namespace, service.Name, err)
		}
	}
	ownerRefToService := metav1.OwnerReference{
		APIVersion:         corev1.SchemeGroupVersion.String(),
		Kind:               "Secret",
		Name:               createdService.Name,
		UID:                createdService.UID,
		BlockOwnerDeletion: &trueVal,
	}

	/*
		Endpoints

		Create endpoints which can point any namespace.
	*/
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:            createdService.Name,
			OwnerReferences: []metav1.OwnerReference{ownerRefToService},
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: e.exposerIP,
					},
				},
				Ports: []corev1.EndpointPort{
					{
						Name: "http",
						// Port that the controller http-01 exposer listens on
						Port:     e.exposerPort,
						Protocol: corev1.ProtocolTCP,
					},
				},
			},
		},
	}
	createdEndpoints, err := e.kubeClientset.CoreV1().Endpoints(e.route.Namespace).Create(endpoints)
	if err != nil {
		if !kapierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create exposing Endpoints %s/%s: %v", e.route.Namespace, endpoints.Name, err)
		}

		glog.Warningf("Forwarding Endpoints %s/%s already exists, forcing rewrite", createdEndpoints.Namespace, createdEndpoints.Name)

		preexistingEndpoints, err := e.kubeClientset.CoreV1().Endpoints(e.route.Namespace).Get(endpoints.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get exposing Endpoints %s/%s before updating: %v", e.route.Namespace, endpoints.Name, err)
		}

		endpoints.ResourceVersion = preexistingEndpoints.ResourceVersion
		createdEndpoints, err = e.kubeClientset.CoreV1().Endpoints(e.route.Namespace).Update(endpoints)
		if err != nil {
			return fmt.Errorf("failed to update exposing Endpoints %s/%s: %v", e.route.Namespace, endpoints.Name, err)
		}
	}

	/*
		Route

		Create Route to accept the traffic for ACME challenge.
	*/
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:            exposingName,
			OwnerReferences: []metav1.OwnerReference{ownerRefToService},
		},
		Spec: routev1.RouteSpec{
			Host: domain,
			Path: c.HTTP01ChallengePath(token),
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: exposingName,
			},
			// TODO: check if setting TLS is still needed or file an issue in origin. I think it was connected
			// to router badly matching subpaths on route for the same domain if only one of them was using TLS
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationEdge,
				InsecureEdgeTerminationPolicy: "Allow",
			},
		},
	}

	createdRoute, err := e.routeClientset.RouteV1().Routes(e.route.Namespace).Create(route)
	if err != nil {
		if !kapierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create exposing Route %s/%s: %v", e.route.Namespace, route.Name, err)
		}

		glog.Warningf("Forwarding Route %s/%s already exists, forcing rewrite", createdRoute.Namespace, createdRoute.Name)

		preexistingRoute, err := e.routeClientset.RouteV1().Routes(e.route.Namespace).Get(route.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get exposing Route %s/%s before updating: %v", e.route.Namespace, route.Name, err)
		}

		route.ResourceVersion = preexistingRoute.ResourceVersion
		createdRoute, err = e.routeClientset.RouteV1().Routes(e.route.Namespace).Update(route)
		if err != nil {
			return fmt.Errorf("failed to update exposing Route %s/%s: %v", e.route.Namespace, route.Name, err)
		}
	}

	glog.V(4).Infof("Waiting for exposing route %s/%s to be admitted.", createdRoute.Namespace, createdRoute.Name)

	if !routeutil.IsAdmitted(createdRoute) {
		// TODO: switch to informer to avoid broken watches
		watcher, err := e.routeClientset.RouteV1().Routes(e.route.Namespace).Watch(metav1.SingleObject(createdRoute.ObjectMeta))
		_, err = watch.Until(RouterAdmitTimeout, watcher, func(event watch.Event) (bool, error) {
			switch event.Type {
			case watch.Modified:
				exposingRoute := event.Object.(*routev1.Route)
				if routeutil.IsAdmitted(exposingRoute) {
					return trueVal, nil
				}

				return false, nil
			default:
				return trueVal, fmt.Errorf("unexpected event type %s while waiting for Route %s/%s to be admitted",
					event.Type, createdRoute.Namespace, createdRoute.Name)
			}
		})
		if err != nil {
			return fmt.Errorf("exceeded timeout %v while waiting for Route %s/%s to be admitted: %v", RouterAdmitTimeout, createdRoute.Namespace, createdRoute.Name, err)
		}
	}
	glog.V(4).Infof("Exposing route %s/%s has been admitted. %#v", createdRoute.Namespace, createdRoute.Name, createdRoute)

	err = e.underlyingExposer.Expose(c, domain, token)
	if err != nil {
		return fmt.Errorf("failed to expose challenge for Route %s/%s: ", e.route.Namespace, e.route.Name)
	}

	// We need to wait for Route to be accessible on the Router because because Route can be admitted but not exposed yet.
	glog.V(4).Infof("Waiting for route %s/%s to be exposed on the router.", createdRoute.Namespace, createdRoute.Name)

	url := "http://" + domain + c.HTTP01ChallengePath(token)
	key, err := c.HTTP01ChallengeResponse(token)
	if err != nil {
		return fmt.Errorf("failed to compute key: %v", err)
	}
	err = wait.ExponentialBackoff(
		wait.Backoff{
			Duration: 1 * time.Second,
			Factor:   1.3,
			Jitter:   0.2,
			Steps:    22,
		},
		func() (bool, error) {
			response, err := http.Get(url)
			if err != nil {
				glog.Errorf("Failed to GET %q: %v", url, err)
				return false, nil
			}

			defer response.Body.Close()

			// No response should be longer that this, we need to prevent against DoS
			buffer := make([]byte, 2048)
			n, err := response.Body.Read(buffer)
			if err != nil && err != io.EOF {
				glog.Errorf("Failed to read response body into buffer: %v", err)
				return false, nil
			}
			body := string(buffer[:n])

			if response.StatusCode != http.StatusOK {
				glog.V(3).Info("Failed to GET %q: %s: %s", url, response.Status, util.FirstNLines(util.MaxNCharacters(body, 160), 5))
				return false, nil
			}

			if body != key {
				glog.V(3).Infof("Key for route %s/%s is not yet exposed.", createdRoute.Namespace, createdRoute.Name)
				return false, nil
			}

			return true, nil
		},
	)
	if err != nil {
		e.recorder.Event(e.route, "Controller failed to verify that exposing Route is accessible. It will continue with ACME validation but chances are that either exposing failed or your domain can't be reached from inside the cluster.", corev1.EventTypeWarning, "ExposingRouteNotVerified")
	} else {
		glog.V(4).Infof("Exposing Route %s/%s is accessible and contains correct response.")
	}

	return nil
}

func (e *Exposer) Remove(c *acme.Client, domain string, token string) error {
	// Name of the forwarding Service and Route
	exposingName := e.exposingTmpName()

	foregroundPolicy := metav1.DeletePropagationForeground

	glog.V(4).Infof("Deleting exposing Service and Route %s/%s.", e.route.Namespace, exposingName)

	// We need to delete only the Service as Route has ownerReference to it and will be GC'd.
	err := e.kubeClientset.CoreV1().Services(e.route.Namespace).Delete(exposingName, &metav1.DeleteOptions{
		PropagationPolicy: &foregroundPolicy,
	})
	if err != nil {
		return err
	}

	return e.underlyingExposer.Remove(c, domain, token)
}
