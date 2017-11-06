package cmd

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"time"

	"github.com/golang/glog"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned"
	routeinformersv1 "github.com/openshift/client-go/route/informers/externalversions/route/v1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	kcoreinformersv1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"context"

	"github.com/tnozicka/openshift-acme/pkg/acme/challengeexposers"
	cmdutil "github.com/tnozicka/openshift-acme/pkg/cmd/util"
	routecontroller "github.com/tnozicka/openshift-acme/pkg/controllers/route"
	"github.com/tnozicka/openshift-acme/pkg/signals"
)

const (
	DefaultLoglevel               = 0
	Flag_LogLevel_Key             = "loglevel"
	Flag_Kubeconfig_Key           = "kubeconfig"
	Flag_Listen_Key               = "listen"
	Flag_Acmeurl_Key              = "acmeurl"
	Flag_Selfservicename_Key      = "selfservicename"
	Flag_Selfservicenamespace_Key = "selfservicenamespace"
	Flag_Namespace_Key            = "namespace"
	ResyncPeriod                  = 10 * time.Minute
	Workers                       = 10
)

func NewOpenShiftAcmeCommand(in io.Reader, out, err io.Writer) *cobra.Command {
	v := viper.New()
	v.SetEnvPrefix("openshift_acme")
	v.AutomaticEnv()
	replacer := strings.NewReplacer("-", "_")
	v.SetEnvKeyReplacer(replacer)

	// Parent command to which all subcommands are added.
	rootCmd := &cobra.Command{
		Use:   "openshift-acme",
		Short: "openshift-acme is a controller for Kubernetes (and OpenShift) which will obtain SSL certificates from ACME provider (like \"Let's Encrypt\")",
		Long:  "openshift-acme is a controller for Kubernetes (and OpenShift) which will obtain SSL certificates from ACME provider (like \"Let's Encrypt\")\n\nFind more information at https://github.com/tnozicka/openshift-acme",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return cmdutil.UsageError(cmd, "Unexpected args: %v", args)
			}

			return RunServer(v, cmd, out)
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// We have to bind Viper in Run because there is only one instance to avoid collisions
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Kubeconfig_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Listen_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Acmeurl_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Selfservicename_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Selfservicenamespace_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Namespace_Key)

			return nil
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	//rootCmd.PersistentFlags().Int8P(Flag_LogLevel_Key, "", 8, "Set loglevel")
	rootCmd.PersistentFlags().StringP(Flag_Kubeconfig_Key, "", "", "Absolute path to the kubeconfig file")
	rootCmd.PersistentFlags().StringP(Flag_Listen_Key, "", "0.0.0.0:5000", "Listen address for http-01 server")
	rootCmd.PersistentFlags().StringP(Flag_Acmeurl_Key, "", "https://acme-staging.api.letsencrypt.org/directory", "ACME URL like https://acme-v01.api.letsencrypt.org/directory")
	rootCmd.PersistentFlags().StringP(Flag_Namespace_Key, "n", "", "Restricts controller to namespace. If not specified controller watches all namespaces.")
	rootCmd.PersistentFlags().StringP(Flag_Selfservicename_Key, "", "acme-controller", "Name of the service pointing to a pod with this program.")
	rootCmd.PersistentFlags().StringP(Flag_Selfservicenamespace_Key, "", "", "Namespace of the service pointing to a pod with this program. Defaults to current namespace this program is running inside; if run outside of the cluster defaults to 'default' namespace")

	from := flag.CommandLine
	if flag := from.Lookup("v"); flag != nil {
		level := flag.Value.(*glog.Level)
		levelPtr := (*int32)(level)
		rootCmd.PersistentFlags().Int32Var(levelPtr, Flag_LogLevel_Key, DefaultLoglevel, "Set the level of log output (0-10)")
		if rootCmd.PersistentFlags().Lookup("v") == nil {
			rootCmd.PersistentFlags().Int32Var(levelPtr, "v", DefaultLoglevel, "Set the level of log output (0-10)")
		}
		rootCmd.PersistentFlags().Lookup("v").Hidden = true
	}
	flag.Set("logtostderr", "true")
	// Make glog happy
	flag.CommandLine.Parse([]string{})

	return rootCmd
}

func getClientConfig(kubeConfigPath string) *restclient.Config {
	if kubeConfigPath == "" {
		glog.Infof("No kubeconfig specified, using InClusterConfig.")
		config, err := restclient.InClusterConfig()
		if err != nil {
			glog.Fatalf("Failed to create InClusterConfig: %v", err)
		}
		return config
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeConfigPath}, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		glog.Fatalf("Failed to create config from kubeConfigPath (%s): %v", kubeConfigPath, err)
	}
	return config
}

func RunServer(v *viper.Viper, cmd *cobra.Command, out io.Writer) error {
	stopCh := signals.StopChannel()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-stopCh
		cancel()
	}()

	acmeUrl := v.GetString(Flag_Acmeurl_Key)
	glog.Infof("ACME server url is '%s'", acmeUrl)

	config := getClientConfig(v.GetString(Flag_Kubeconfig_Key))

	kubeClientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to build kubernetes clientset: %v", err)
	}

	routeClientset, err := routeclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to build route clientset: %v", err)
	}

	namespace := v.GetString(Flag_Namespace_Key)
	if namespace == "" {
		glog.Info("Watching all namespaces.")
	} else {
		glog.Infof("Watching only namespace %q.", namespace)
	}

	selfServiceName := v.GetString(Flag_Selfservicename_Key)
	if selfServiceName == "" {
		return fmt.Errorf("%q can't be empty", Flag_Selfservicename_Key)
		// TODO: try bootstraping by (only this podIP) -> Endpoint -> Service
	}

	selfServiceNamespace := v.GetString(Flag_Selfservicenamespace_Key)
	if selfServiceNamespace == "" {
		glog.V(4).Infof("%q is unspecified, trying inCluster", Flag_Selfservicenamespace_Key)
		selfServiceNamespaceBytes, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			return fmt.Errorf("failed to detect selfServiceNamespace: %v", err)
		}
		selfServiceNamespace = (string)(selfServiceNamespaceBytes)
	}

	//accountSecret, err := kubeClientset.CoreV1().Secrets(selfServiceNamespace).Get()

	routeInformer := routeinformersv1.NewRouteInformer(routeClientset, namespace, ResyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	glog.Infof("Starting Route informer")
	go routeInformer.Run(stopCh)

	secretInformer := kcoreinformersv1.NewSecretInformer(kubeClientset, namespace, ResyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	glog.Infof("Starting Secret informer")
	go secretInformer.Run(stopCh)

	exposer, err := challengeexposers.NewHttp01(ctx, ":80")
	if err != nil {
		return err
	}

	rc := routecontroller.NewRouteController(exposer, routeClientset, kubeClientset, routeInformer, secretInformer, selfServiceNamespace, selfServiceName)
	go rc.Run(Workers, stopCh)

	<-stopCh

	// TODO: We should wait for controllers to stop

	glog.Flush()

	return nil
}
