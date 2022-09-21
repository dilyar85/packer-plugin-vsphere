//go:generate packer-sdc struct-markdown
//go:generate packer-sdc mapstructure-to-hcl2 -type ConnectSupervisorConfig

package supervisor

import (
	"context"
	"os"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/pkg/errors"
)

const (
	StateKeySupervisorNamespace = "supervisor_namespace"
	StateKeyKubeClientSet       = "kube_client_set"
	StateKeyKubeDynamicClient   = "kube_dynamic_client"
)

type ConnectSupervisorConfig struct {
	// The path to kubeconfig file for accessing to the vSphere Supervisor cluster. Defaults to the value of `KUBECONFIG` envvar or `$HOME/.kube/config` if the envvar is not set.
	KubeconfigPath string `mapstructure:"kubeconfig_path"`
	// The Supervisor namespace to deploy the source VM. Defaults to the current context's namespace in kube config.
	SupervisorNamespace string `mapstructure:"supervisor_namespace"`
}

func (c *ConnectSupervisorConfig) Prepare() []error {
	// Set the kubeconfig path from KUBECONFIG env var or the default path if not provided.
	if c.KubeconfigPath == "" {
		if val := os.Getenv(clientcmd.RecommendedConfigPathEnvVar); val != "" {
			c.KubeconfigPath = val
		} else {
			c.KubeconfigPath = clientcmd.RecommendedHomeFile
		}
	}

	// Set the Supervisor namespace from current context if not provided.
	if c.SupervisorNamespace == "" {
		data, err := os.ReadFile(c.KubeconfigPath)
		if err != nil {
			return []error{errors.Wrap(err, "failed to read kubeconfig file")}
		}
		kubeConfig, err := clientcmd.NewClientConfigFromBytes(data)
		if err != nil {
			return []error{errors.Wrap(err, "failed to parse kubeconfig file")}
		}
		ns, _, err := kubeConfig.Namespace()
		if err != nil {
			return []error{errors.Wrap(err, "failed to get current context's namespace in kubeconfig file")}
		}

		c.SupervisorNamespace = ns
	}

	return nil
}

type StepConnectSupervisor struct {
	Config *ConnectSupervisorConfig
}

func (s *StepConnectSupervisor) getKubeClients() (*kubernetes.Clientset, dynamic.Interface, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.Config.KubeconfigPath)
	if err != nil {
		return nil, nil, err
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	return clientSet, dynamicClient, nil
}

func (s *StepConnectSupervisor) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	logger := state.Get("logger").(*PackerLogger)
	logger.Info("Connecting to Supervisor cluster...")

	clientSet, dynamicClient, err := s.getKubeClients()
	if err != nil {
		state.Put("error", err)
		return multistep.ActionHalt
	}
	state.Put(StateKeyKubeClientSet, clientSet)
	state.Put(StateKeyKubeDynamicClient, dynamicClient)
	state.Put(StateKeySupervisorNamespace, s.Config.SupervisorNamespace)

	logger.Info("Successfully connected to Supervisor cluster")
	return multistep.ActionContinue
}

func (s *StepConnectSupervisor) Cleanup(multistep.StateBag) {}
