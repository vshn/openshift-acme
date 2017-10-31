package framework

import (
	"fmt"
	"io/ioutil"
	"strings"

	g "github.com/onsi/ginkgo"
	o "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
	kclientcmd "k8s.io/client-go/tools/clientcmd"

	oclientcmd "github.com/openshift/origin/pkg/cmd/util/clientcmd"
	"github.com/openshift/origin/pkg/cmd/util/tokencmd"
	"github.com/openshift/origin/pkg/oc/cli/config"
	projectclientset "github.com/openshift/origin/pkg/project/generated/clientset"
	routeclientset "github.com/openshift/origin/pkg/route/generated/clientset"
)

type Framework struct {
	name               string
	namespace          *v1.Namespace
	namespacesToDelete []*v1.Namespace

	clientConfig      *rest.Config
	adminClientConfig *rest.Config
	username          string
}

func NewFramework(project string) *Framework {
	uniqueProject := names.SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", project))
	f := &Framework{
		name:     uniqueProject,
		username: "admin",
	}

	g.BeforeEach(f.BeforeEach)
	g.AfterEach(f.AfterEach)

	return f
}

func (f *Framework) Namespace() string {
	return f.namespace.Name
}

func (f *Framework) AddNamespace(namespace *v1.Namespace) {
	f.namespace = namespace
	f.namespacesToDelete = append(f.namespacesToDelete, namespace)
}

func (f *Framework) Username() string {
	return f.username
}

func (f *Framework) AdminClientConfig() *rest.Config {
	if f.adminClientConfig == nil {
		var err error
		f.adminClientConfig, err = kclientcmd.NewNonInteractiveDeferredLoadingClientConfig(&kclientcmd.ClientConfigLoadingRules{ExplicitPath: TestContext.KubeConfigPath}, &kclientcmd.ConfigOverrides{}).ClientConfig()
		o.Expect(err).NotTo(o.HaveOccurred())
	}

	return f.adminClientConfig
}

func (f *Framework) ClientConfig() *rest.Config {
	if f.clientConfig == nil {
		f.clientConfig = f.AdminClientConfig()
	}

	return f.clientConfig
}

func (f *Framework) KubeClientSet() *kubernetes.Clientset {
	clientSet, err := kubernetes.NewForConfig(f.ClientConfig())
	o.Expect(err).NotTo(o.HaveOccurred())
	return clientSet
}

func (f *Framework) KubeAdminClientSet() *kubernetes.Clientset {
	clientSet, err := kubernetes.NewForConfig(f.AdminClientConfig())
	o.Expect(err).NotTo(o.HaveOccurred())
	return clientSet
}

func (f *Framework) CreateNamespace(name string, labels map[string]string) (*v1.Namespace, error) {
	createTestingNS := TestContext.CreateTestingNS
	if createTestingNS == nil {
		createTestingNS = CreateTestingNamespace
	}

	if labels == nil {
		labels = map[string]string{}
	}
	labels["e2e"] = "openshift-acme"

	ns, err := createTestingNS(f, name, labels)
	if err != nil {
		return nil, err
	}

	f.AddNamespace(ns)

	return ns, nil
}

func (f *Framework) ChangeUser(username string, namespace string) {
	if username == "admin" {
		f.clientConfig = f.AdminClientConfig()
		return
	}

	token, err := tokencmd.RequestToken(f.AdminClientConfig(), nil, username, "password")
	o.Expect(err).NotTo(o.HaveOccurred())

	clientConfig := oclientcmd.AnonymousClientConfig(f.AdminClientConfig())
	clientConfig.BearerToken = token

	f.clientConfig = &clientConfig

	kubeConfig, err := config.CreateConfig(namespace, f.clientConfig)
	o.Expect(err).NotTo(o.HaveOccurred())

	tmpFile, err := ioutil.TempFile("", fmt.Sprintf("%s-kubeconfig-", username))
	o.Expect(err).NotTo(o.HaveOccurred())

	err = kclientcmd.WriteToFile(*kubeConfig, tmpFile.Name())
	o.Expect(err).NotTo(o.HaveOccurred())

	f.username = username
	Logf("ConfigPath is now %q", tmpFile.Name())
}

func (f *Framework) BeforeEach() {
	g.By("Building a namespace api object")
	_, err := f.CreateNamespace(f.name, nil)
	o.Expect(err).NotTo(o.HaveOccurred())
}

func (f *Framework) AfterEach() {
	defer func() {
		nsDeletionErrors := map[string]error{}

		if TestContext.DeleteTestingNSPolicy == DeleteTestingNSPolicyNever ||
			(TestContext.DeleteTestingNSPolicy == DeleteTestingNSPolicyOnSuccess && !g.CurrentGinkgoTestDescription().Failed) {
			return
		}

		for _, ns := range f.namespacesToDelete {
			g.By(fmt.Sprintf("Destroying namespace %q.", ns.Name))
			var gracePeriod int64 = 0
			err := f.KubeAdminClientSet().CoreV1().Namespaces().Delete(ns.Name, &metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod})
			if err != nil {
				nsDeletionErrors[ns.Name] = err
			}
		}

		// Prevent reuse
		f.namespace = nil
		f.namespacesToDelete = nil

		if len(nsDeletionErrors) > 0 {
			messages := []string{}
			for namespaceKey, namespaceErr := range nsDeletionErrors {
				messages = append(messages, fmt.Sprintf("Couldn't delete ns: %q: %s (%#v)", namespaceKey, namespaceErr, namespaceErr))
			}
			Failf(strings.Join(messages, ","))
		}
	}()

	// Print events if the test failed.
	if g.CurrentGinkgoTestDescription().Failed {
		for _, ns := range f.namespacesToDelete {
			g.By(fmt.Sprintf("Collecting events from namespace %q.", ns.Name))
			DumpEventsInNamespace(f.KubeClientSet(), ns.Name)
		}
	}
}

func (f *Framework) RouteClientset() routeclientset.Interface {
	clientset, err := routeclientset.NewForConfig(f.ClientConfig())
	o.Expect(err).NotTo(o.HaveOccurred(), "Failed to create route clientset")

	return clientset
}

func (f *Framework) ProjectClientset() projectclientset.Interface {
	clientset, err := projectclientset.NewForConfig(f.ClientConfig())
	o.Expect(err).NotTo(o.HaveOccurred(), "Failed to create project clientset")

	return clientset
}
