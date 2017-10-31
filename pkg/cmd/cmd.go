package cmd

import (
	//"context"
	"errors"
	"io"
	//"io/ioutil"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	//"github.com/tnozicka/openshift-acme/pkg/acme"
	//"github.com/tnozicka/openshift-acme/pkg/acme/challengeexposers"
	cmdutil "github.com/tnozicka/openshift-acme/pkg/cmd/util"
	routecontroller "github.com/tnozicka/openshift-acme/pkg/openshift/controllers/route"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	//kcoreinformersv1 "k8s.io/client-go/informers/core/v1"
	"time"

	kinformers "k8s.io/client-go/informers"
)

const (
	Flag_LogLevel_Key             = "loglevel"
	Flag_Kubeconfig_Key           = "kubeconfig"
	Flag_Listen_Key               = "listen"
	Flag_Acmeurl_Key              = "acmeurl"
	Flag_Selfservicename_Key      = "selfservicename"
	Flag_Selfservicenamespace_Key = "selfservicenamespace"
	Flag_Watchnamespace_Key       = "watch-namespace"
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
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_LogLevel_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Kubeconfig_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Listen_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Acmeurl_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Selfservicename_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Selfservicenamespace_Key)
			cmdutil.BindViper(v, cmd.PersistentFlags(), Flag_Watchnamespace_Key)

			// Setup logger
			//err := glog.V() Level.Set(v.GetString(Flag_LogLevel_Key))
			//if err != nil {
			//	return nil
			//}

			return nil
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.PersistentFlags().Int8P(Flag_LogLevel_Key, "", 8, "Set loglevel")
	rootCmd.PersistentFlags().StringP(Flag_Kubeconfig_Key, "", "", "Absolute path to the kubeconfig file")
	rootCmd.PersistentFlags().StringP(Flag_Listen_Key, "", "0.0.0.0:5000", "Listen address for http-01 server")
	rootCmd.PersistentFlags().StringP(Flag_Acmeurl_Key, "", "https://acme-staging.api.letsencrypt.org/directory", "ACME URL like https://acme-v01.api.letsencrypt.org/directory")
	rootCmd.PersistentFlags().StringP(Flag_Selfservicename_Key, "", "acme-controller", "Name of the service pointing to a pod with this program.")
	rootCmd.PersistentFlags().StringSliceP(Flag_Watchnamespace_Key, "w", []string{""}, "Restrics controller to namespace. If not specified controller watches for routes accross namespaces.")
	rootCmd.PersistentFlags().StringP(Flag_Selfservicenamespace_Key, "", "", "Namespace of the service pointing to a pod with this program. Defaults to current namespace this program is running inside; if run outside of the cluster defaults to 'default' namespace")

	return rootCmd
}

func getClientConfig(kubeConfigPath string) *restclient.Config {
	if kubeConfigPath == "" {
		config, err := restclient.InClusterConfig()
		if err != nil {
			glog.Fatalf("Failed to create InClusterConfig: %v", err)
		}
		return config
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeConfigPath}, nil).ClientConfig()
	if err != nil {
		glog.Fatalf("Failed to create config from kubeConfigPath (%s): %v", kubeConfigPath, err)
	}
	return config
}

func RunServer(v *viper.Viper, cmd *cobra.Command, out io.Writer) error {
	// Setup signal handling
	signalChannel := make(chan os.Signal, 10)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGABRT, syscall.SIGTERM)

	//ctx, cancel := context.WithCancel(context.Background())
	//defer cancel()

	acmeUrl := v.GetString(Flag_Acmeurl_Key)
	glog.Infof("ACME server url is '%s'", acmeUrl)

	config := getClientConfig(v.GetString(Flag_Kubeconfig_Key))

	kubeClientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		glog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	kubeInformerFactory := kinformers.NewSharedInformerFactory(kubeClientset, 5*time.Minute)

	routeController := routecontroller.NewRouteController(kubeClientset, kubeInformerFactory)

	go kubeInformerFactory.Start(stopCh)

	//watchNamespaces := v.GetStringSlice(Flag_Watchnamespace_Key)
	//// spf13/cobra (sadly) treats []string{""} as []string{} => we need to fix it!
	//if len(watchNamespaces) == 0 {
	//	watchNamespaces = []string{""}
	//}
	//glog.Infof("namespaces: %#v", watchNamespaces)

	//selfServiceNamespace := v.GetString(Flag_Selfservicenamespace_Key)
	//if selfServiceNamespace == "" {
	//	namespace, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	//	if err != nil {
	//		selfServiceNamespace = "default"
	//		glog.Warningf("Unable to autodetect service namespace. Defaulting to namespace '%s'. Error: %s", selfServiceNamespace, err)
	//	} else {
	//		selfServiceNamespace = string(namespace)
	//	}
	//}
	//selfService := route_controller.ServiceID{
	//	Name:      v.GetString(Flag_Selfservicename_Key),
	//	Namespace: selfServiceNamespace,
	//}
	//rc, err := route_controller.NewRouteController(ctx, clientset.CoreV1(), ac, challengeExposers, selfService, watchNamespaces)
	//if err != nil {
	//	glog.Errorf("Couln't initialize RouteController: '%s'", err)
	//	return err
	//}
	//glog.Info("RouteController initializing")
	//rc.Start()
	//defer rc.Wait()
	//defer cancel()
	//glog.Info("RouteController started")

	acDone := make(chan struct{}, 1)
	go func() {
		//ac.Wait()
		acDone <- struct{}{}
	}()

	rcDone := make(chan struct{}, 1)
	go func() {
		//rc.Wait()
		rcDone <- struct{}{}
	}()

	select {
	case <-acDone:
		return errors.New("AcmeController ended unexpectedly!")
	case <-rcDone:
		return errors.New("RouteController ended unexpectedly!")
	case s := <-signalChannel:
		glog.Infof("Cancelling due to signal '%s'", s)
		return nil
	}
}
