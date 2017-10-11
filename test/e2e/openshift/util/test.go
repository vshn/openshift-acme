package util

import (
	"os"
	"testing"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	"github.com/tnozicka/openshift-acme/test/e2e/framework"
)

var TestContext *framework.TestContextType = &framework.TestContext

func KubeConfigPath() string {
	return os.Getenv("KUBECONFIG")
}

func InitTest() {

	TestContext.KubeConfigPath = KubeConfigPath()
	framework.Logf("KubeConfigPath: %q", TestContext.KubeConfigPath)
	if TestContext.KubeConfigPath == "" {
		framework.Failf("You have to specify KubeConfigPath. (Use KUBECONFIG environment variable.)")
	}

	TestContext.CreateTestingNS = framework.CreateTestingProjectAndChangeUser

	switch p := framework.DeleteTestingNSPolicyType(os.Getenv("DELETE_NS_POLICY")); p {
	case framework.DeleteTestingNSPolicyAlways,
		framework.DeleteTestingNSPolicyOnSuccess,
		framework.DeleteTestingNSPolicyNever:
		TestContext.DeleteTestingNSPolicy = framework.DeleteTestingNSPolicyType(p)
	case "":
		TestContext.DeleteTestingNSPolicy = framework.DeleteTestingNSPolicyAlways
	default:
		framework.Failf("Invalid DeleteTestingNSPolicy: %q", TestContext.DeleteTestingNSPolicy)
	}
}

func ExecuteTest(t *testing.T, suite string) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, suite)
}
