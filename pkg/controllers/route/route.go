package route

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/golang/glog"
	routev1 "github.com/openshift/api/route/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned"
	routelistersv1 "github.com/openshift/client-go/route/listers/route/v1"
	corev1 "k8s.io/api/core/v1"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	kcorelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	"github.com/tnozicka/openshift-acme/pkg/acme/challengeexposers"
	"github.com/tnozicka/openshift-acme/pkg/api"
	"github.com/tnozicka/openshift-acme/pkg/cert"
	routeutil "github.com/tnozicka/openshift-acme/pkg/route"
	"github.com/tnozicka/openshift-acme/pkg/util"
)

const (
	ControllerName           = "openshift-acme-controller"
	MaxRetries               = 5
	RenewalStandardDeviation = 1
	RenewalMean              = 0
)

var (
	KeyFunc = cache.DeletionHandlingMetaNamespaceKeyFunc
)

type RouteController struct {
	// TODO: switch this for generic interface to allow other types like DNS01
	exposer *challengeexposers.Http01

	routeIndexer cache.Indexer

	routeClientset routeclientset.Interface
	kubeClientset  kubernetes.Interface

	routeInformer  cache.SharedIndexInformer
	secretInformer cache.SharedIndexInformer

	routeLister  routelistersv1.RouteLister
	secretLister kcorelistersv1.SecretLister

	// routeInformerSynced returns true if the Route store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	routeInformerSynced cache.InformerSynced

	// secretInformerSynced returns true if the Secret store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	secretInformerSynced cache.InformerSynced

	eventRecorder record.EventRecorder

	queue workqueue.RateLimitingInterface

	selfServiceNamespace, selfServiceName string
}

func NewRouteController(
	exposer *challengeexposers.Http01,
	routeClientset routeclientset.Interface,
	kubeClientset kubernetes.Interface,
	routeInformer cache.SharedIndexInformer,
	secretInformer cache.SharedIndexInformer,
	selfServiceNamespace, selfServiceName string,
) *RouteController {

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeClientset.CoreV1().Events("")})

	rc := &RouteController{
		exposer: exposer,

		routeIndexer: routeInformer.GetIndexer(),

		routeClientset: routeClientset,
		kubeClientset:  kubeClientset,

		routeInformer:  routeInformer,
		secretInformer: secretInformer,

		routeLister:  routelistersv1.NewRouteLister(routeInformer.GetIndexer()),
		secretLister: kcorelistersv1.NewSecretLister(secretInformer.GetIndexer()),

		routeInformerSynced:  routeInformer.HasSynced,
		secretInformerSynced: secretInformer.HasSynced,

		eventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: ControllerName}),

		queue: workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),

		selfServiceNamespace: selfServiceNamespace,
		selfServiceName:      selfServiceName,
	}

	routeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    rc.addRoute,
		UpdateFunc: rc.updateRoute,
		DeleteFunc: rc.deleteRoute,
	})
	//secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
	//	AddFunc:    rc.addSecret,
	//	UpdateFunc: rc.updateSecret,
	//	DeleteFunc: rc.deleteSecret,
	//})

	return rc
}

func (rc *RouteController) enqueueRoute(route *routev1.Route) {
	key, err := KeyFunc(route)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Couldn't get key for object %#v: %v", route, err))
		return
	}

	rc.queue.Add(key)
}

func (rc *RouteController) addRoute(obj interface{}) {
	route := obj.(*routev1.Route)
	if !util.IsManaged(route) {
		glog.V(5).Infof("Skipping Route %s/%s UID=%s RV=%s", route.Namespace, route.Name, route.UID, route.ResourceVersion)
		return
	}

	glog.V(4).Infof("Adding Route %s/%s UID=%s RV=%s", route.Namespace, route.Name, route.UID, route.ResourceVersion)
	rc.enqueueRoute(route)
}

func (rc *RouteController) updateRoute(old, cur interface{}) {
	oldRoute := old.(*routev1.Route)
	newRoute := cur.(*routev1.Route)

	// A periodic relist will send update events for all known configs.
	if newRoute.ResourceVersion == oldRoute.ResourceVersion {
		return
	}

	if !util.IsManaged(newRoute) {
		glog.V(5).Infof("Skipping Route %s/%s UID=%s RV=%s", newRoute.Namespace, newRoute.Name, newRoute.UID, newRoute.ResourceVersion)
		return
	}

	glog.V(4).Infof("Updating Route from %s/%s UID=%s RV=%s to %s/%s UID=%s,RV=%s",
		oldRoute.Namespace, oldRoute.Name, oldRoute.UID, oldRoute.ResourceVersion,
		newRoute.Namespace, newRoute.Name, newRoute.UID, newRoute.ResourceVersion)

	rc.enqueueRoute(newRoute)
}

func (rc *RouteController) deleteRoute(obj interface{}) {
	route, ok := obj.(*routev1.Route)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("object is not a Route neither tombstone: %#v", obj))
			return
		}
		route, ok = tombstone.Obj.(*routev1.Route)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a Route %#v", obj))
			return
		}
	}

	if !util.IsManaged(route) {
		glog.V(5).Infof("Skipping Route %s/%s UID=%s RV=%s", route.Namespace, route.Name, route.UID, route.ResourceVersion)
		return
	}

	glog.V(4).Infof("Deleting Route %s/%s UID=%s RV=%s", route.Namespace, route.Name, route.UID, route.ResourceVersion)
	rc.enqueueRoute(route)
}

// TODO: extract this function to be re-used by ingress controller
func (rc *RouteController) getState(t time.Time, route *routev1.Route) api.AcmeState {
	if route.Annotations != nil {
		_, ok := route.Annotations[api.AcmeAwaitingAuthzUrlAnnotation]
		if ok {
			return api.AcmeAwaitingAuthzUrlAnnotation
		}
	}

	if route.Spec.TLS == nil {
		return api.AcmeStateNeedsCert
	}

	certPemData := &cert.CertPemData{
		Key: []byte(route.Spec.TLS.Key),
		Crt: []byte(route.Spec.TLS.Certificate),
	}
	certificate, err := certPemData.Certificate()
	if err != nil {
		glog.Errorf("Failed to decode certificate from route %s/%s", route.Namespace, route.Name)
		return api.AcmeStateNeedsCert
	}

	err = certificate.VerifyHostname(route.Spec.Host)
	if err != nil {
		glog.Errorf("Certificate is invalid for route %s/%s with hostname %q", route.Namespace, route.Name, route.Spec.Host)
		return api.AcmeStateNeedsCert
	}

	if !cert.IsValid(certificate, t) {
		return api.AcmeStateNeedsCert
	}

	// We need to trigger renewals before the certs expire
	remains := certificate.NotAfter.Sub(t)
	lifetime := certificate.NotAfter.Sub(certificate.NotBefore)

	// This is the deadline when we start renewing
	if remains <= lifetime/3 {
		glog.Infof("Renewing cert because we reached a deadline of %s", remains)
		return api.AcmeStateNeedsCert
	}

	// In case many certificates were provisioned at specific time
	// We will try to avoid spikes by renewing randomly
	if remains <= lifetime/2 {
		// We need to randomize renewals to spread the load.
		// Closer to deadline, bigger chance
		s := rand.NewSource(t.UnixNano())
		r := rand.New(s)
		n := r.NormFloat64()*RenewalStandardDeviation + RenewalMean
		// We use left half of normal distribution (all negative numbers).
		if n < 0 {
			glog.V(4).Infof("Renewing cert in advance with %s remaining to spread the load.", remains)
			return api.AcmeStateNeedsCert
		}
	}

	return api.AcmeStateOk
}

func (rc *RouteController) expose(route *routev1.Route) error {
	// Name of the forwarding Service and Route
	name := fmt.Sprintf("%s-%s", route.Name, api.ForwardingRouteSuffing)

	true := true
	// Forwarding Route and Service share ObjectMeta
	meta := metav1.ObjectMeta{
		Name: name,
		OwnerReferences: []metav1.OwnerReference{
			{
				APIVersion:         route.APIVersion,
				Kind:               route.Kind,
				Name:               route.Name,
				UID:                route.UID,
				Controller:         &true,
				BlockOwnerDeletion: &true,
			},
		},
	}

	// Route can only point to a Service in the same namespace
	// but we need to redirect ACME challenge to the ACME service
	// usually deployed in a different namespace.
	// We avoid this limitation by creating a forwarding service.
	// Note that this requires working DNS in your cluster.
	s := &corev1.Service{
		ObjectMeta: meta,
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeExternalName,
			// TODO: Autodetect cluster suffix or fix Kubernetes to correctly resolve cluster QDN instead of FQDN only
			ExternalName: fmt.Sprintf("%s.%s.svc.cluster.local.", rc.selfServiceName, rc.selfServiceNamespace),
		},
	}
	createS, err := rc.kubeClientset.CoreV1().Services(route.Namespace).Create(s)
	if err != nil {
		if !kapierrors.IsAlreadyExists(err) {
			return err
		}

		glog.Errorf("Forwarding Service %s/%s already exists, forcing rewrite")
		s.ResourceVersion = createS.ResourceVersion
		_, err = rc.kubeClientset.CoreV1().Services(route.Namespace).Update(s)
	}

	// Create Route to accept the traffic for ACME challenge
	r := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         route.APIVersion,
					Kind:               route.Kind,
					Name:               route.Name,
					UID:                route.UID,
					Controller:         &true,
					BlockOwnerDeletion: &true,
				},
			},
		},
	}

	createR, err := rc.routeClientset.RouteV1().Routes(route.Namespace).Create(r)
	if err != nil {
		if !kapierrors.IsAlreadyExists(err) {
			return err
		}

		glog.Errorf("Forwarding Route %s/%s already exists, forcing rewrite")
		r.ResourceVersion = createR.ResourceVersion
		_, err = rc.routeClientset.RouteV1().Routes(route.Namespace).Update(r)
	}

	return nil
}

// handle is the business logic of the controller.
// In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
// This function is not meant to be invoked concurrently with the same key.
// TODO: extract common parts to be re-used by ingress controller
func (rc *RouteController) handle(key string) error {
	startTime := time.Now()
	glog.V(4).Infof("Started syncing Route %q (%v)", key, startTime)
	defer func() {
		glog.V(4).Infof("Finished syncing Route %q (%v)", key, time.Since(startTime))
	}()

	objReadOnly, exists, err := rc.routeIndexer.GetByKey(key)
	if err != nil {
		glog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exists {
		glog.V(4).Infof("Route %s does not exist anymore\n", key)
		return nil
	}

	// Deep copy to avoid mutating the cache
	routeReadOnly := objReadOnly.(*routev1.Route)

	// We have to check if Route is admitted to be sure it owns the domain!
	if !routeutil.IsAdmitted(routeReadOnly) {
		glog.V(4).Infof("Skipping Route %s/%s because it's not admitted", key)
		return nil
	}

	state := rc.getState(startTime, routeReadOnly)
	switch state {
	case api.AcmeStateNeedsCert:
		// TODO: Add TTL based lock to allow only one domain to enter this stage
		rc.exposer.Expose()
		rc.expose(routeReadOnly)
	case api.AcmeStateWaitingForAuthz:
	case api.AcmeStateOk:
	default:
		return fmt.Errorf("Failed to determine state for Route: %#v", routeReadOnly)
	}

	route := routeReadOnly.DeepCopy()

	glog.Infof("%v", route)

	return nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (rc *RouteController) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		rc.queue.Forget(key)
		return
	}

	if rc.queue.NumRequeues(key) < MaxRetries {
		glog.Infof("Error syncing Route %v: %v", key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		rc.queue.AddRateLimited(key)
		return
	}

	rc.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	runtime.HandleError(err)
	glog.Infof("Dropping Route %q out of the queue: %v", key, err)
}

func (rc *RouteController) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := rc.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two Routes with the same key are never processed in
	// parallel.
	defer rc.queue.Done(key)

	// Invoke the method containing the business logic
	err := rc.handle(key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	rc.handleErr(err, key)
	return true
}

func (rc *RouteController) runWorker() {
	for rc.processNextItem() {
	}
}

func (rc *RouteController) Run(workers int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()

	// Let the workers stop when we are done
	defer rc.queue.ShutDown()

	glog.Info("Starting Route controller")

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, rc.routeInformerSynced, rc.secretInformerSynced) {
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	for i := 0; i < workers; i++ {
		go wait.Until(rc.runWorker, time.Second, stopCh)
	}

	<-stopCh

	glog.Info("Stopping Route controller")
}
